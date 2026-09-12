// Package host 连接 EE 身份服务与宿主路由：替换管理鉴权、导入旧管理员、兼容 WS/配置/临时令牌；不实现账号业务。
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/temptoken"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

type principalKey struct{}

// consoleSessions 是管理鉴权需要的会话能力，由 identity.SessionService 满足；本包按实际需要声明，不持有整个身份服务。
type consoleSessions interface {
	Authenticate(context.Context, string) (identity.Principal, error)
	ValidateSession(context.Context, string) (identity.Principal, error)
	RequireAccountManager(context.Context, identity.Principal) error
	ConsumeTicket(context.Context, string) (identity.Principal, error)
	Me(context.Context, identity.Principal) (identity.Account, error)
}

// legacyImporter 是导入旧管理员需要的能力，由 identity.AccountService 满足。
type legacyImporter interface {
	State(context.Context) (identity.State, error)
	BootstrapLegacy(ctx context.Context, username, passwordHash string) error
}

// AuthAdapter 实现宿主的 ConsoleAuthProvider；依赖由 app 装配后注入，宿主对象只用于临时令牌与配置读取。
type AuthAdapter struct {
	service consoleSessions
	http    *identityhttp.Handler
	host    *server.BifrostHTTPServer
}

// NewAuthAdapter 由 app 在身份服务与 HTTP 适配构造完成后调用；只接收会话线，建号、重置、恢复对本包不可见。
func NewAuthAdapter(host *server.BifrostHTTPServer, sessions consoleSessions, handler *identityhttp.Handler) *AuthAdapter {
	return &AuthAdapter{service: sessions, http: handler, host: host}
}

// Logger 是本包需要的日志能力，由 app 注入宿主 logger；消息为 printf 风格。
type Logger interface {
	Info(msg string, args ...any)
}

// ImportLegacyAdministrator 在身份未初始化时，把宿主已解析的旧管理员凭据导入为主管理员；已初始化时不读取旧配置。
// 宿主会忽略残缺的文件凭据，所以先直接检查配置文件：残缺或未解析的输入拒绝启动，不能当作空实例开放初始化。
func ImportLegacyAdministrator(ctx context.Context, host *server.BifrostHTTPServer, accounts legacyImporter, log Logger) error {
	state, err := accounts.State(ctx)
	if err != nil {
		return err
	}
	if state.Initialized {
		return nil
	}
	if err := validateLegacyFile(server.GetDefaultConfigDir(host.AppDir)); err != nil {
		return err
	}
	stored, err := host.Config.ConfigStore.GetAuthConfig(ctx)
	if err != nil {
		return identity.ErrUnavailable
	}
	// 文件优先于数据库；宿主在文件密码无法哈希时会回退到完整的旧数据库账号。
	effective := stored
	if host.Config.GovernanceConfig != nil && host.Config.GovernanceConfig.AuthConfig != nil {
		effective = host.Config.GovernanceConfig.AuthConfig
	}
	if effective == nil {
		return nil
	}
	username, hash := credential(effective)
	if username == "" || hash == "" {
		return errors.New("legacy administrator credentials are incomplete")
	}
	if err := accounts.BootstrapLegacy(ctx, username, hash); err != nil {
		// 只有凭据本身不合法才提示去修旧凭据；存储故障等原因照原样带出（identity.Error 已脱敏），
		// 否则运维会被这句话引向错误的方向。
		if errors.Is(err, identity.ErrInvalid) {
			return errors.New("legacy administrator password is not a supported bcrypt hash")
		}
		return fmt.Errorf("legacy administrator import failed: %w", err)
	}
	if storedUser, storedHash := credential(stored); storedUser == username && storedHash == hash {
		log.Info("EE identity: imported resolved host administrator snapshot matching stored database credentials")
	} else {
		log.Info("EE identity: imported resolved host administrator snapshot; stored legacy credentials differ")
	}
	return nil
}

// credential 取宿主 AuthConfig 中已解析的用户名与密码哈希，任一缺失返回空串。
func credential(a *configstore.AuthConfig) (username, hash string) {
	if a == nil || a.AdminUserName == nil || a.AdminPassword == nil {
		return "", ""
	}
	return a.AdminUserName.GetValue(), a.AdminPassword.GetValue()
}

// validateLegacyFile 检查配置文件里显式写出的旧管理员凭据是否完整；哈希与来源选择仍由宿主负责。
func validateLegacyFile(appDir string) error {
	b, err := os.ReadFile(filepath.Join(appDir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot verify legacy authentication input")
	}
	var input struct {
		Auth       *configstore.AuthConfig `json:"auth_config"`
		Governance *struct {
			Auth *configstore.AuthConfig `json:"auth_config"`
		} `json:"governance"`
	}
	if json.Unmarshal(b, &input) != nil {
		return errors.New("cannot parse legacy authentication input")
	}
	a := input.Auth
	if input.Governance != nil && input.Governance.Auth != nil {
		a = input.Governance.Auth
	}
	if a != nil {
		if username, hash := credential(a); username == "" || hash == "" {
			return errors.New("legacy administrator credentials are incomplete or unresolved")
		}
	}
	return nil
}

// RegisterSessionRoutes 用身份路由替换宿主的旧会话路由。
func (a *AuthAdapter) RegisterSessionRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	a.http.RegisterRoutes(r, m...)
}

// public 列出无需管理身份的 GET 入口：健康检查、版本、静态资源及各协议自行验证的 OAuth 发现路径。
func public(method, path string) bool {
	if method != fasthttp.MethodGet {
		return false
	}
	switch path {
	case "/health", "/api/version", "/api/oauth/callback", "/.well-known/jwks.json", "/.well-known/oauth-protected-resource", "/.well-known/oauth-authorization-server":
		return true
	}
	return strings.HasPrefix(path, "/.well-known/oauth-protected-resource/") || strings.HasPrefix(path, "/.well-known/oauth-authorization-server/") || strings.HasPrefix(path, "/api/skills/serve/")
}

// temporaryRoute 精确匹配 handlers/temptokens.go 定义的六组 MCP/OAuth 用户流程路由，这些路由可凭临时令牌访问。
func temporaryRoute(method, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 || parts[0] != "api" {
		return false
	}
	if parts[1] == "oauth" && parts[2] == "per-user" && parts[3] == "flows" && parts[4] != "" {
		return method == fasthttp.MethodGet && (len(parts) == 5 || (len(parts) == 6 && parts[5] == "start"))
	}
	return len(parts) == 5 && parts[4] != "" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) &&
		((parts[1] == "mcp" && parts[2] == "per-user-headers" && parts[3] == "flows") || (parts[1] == "oauth2" && parts[2] == "consent" && parts[3] == "flows"))
}

// authorizeTemporary 按宿主临时令牌服务验证 X-Bifrost-Temp-Token，成功时清除任何 EE 身份并写入令牌范围。
func (a *AuthAdapter) authorizeTemporary(c *fasthttp.RequestCtx) error {
	if a.host == nil || a.host.TempTokens == nil {
		return identity.ErrUnauthorized
	}
	config, err := a.host.Config.ConfigStore.GetClientConfig(c)
	if err != nil {
		return identity.ErrUnavailable
	}
	if config == nil || !config.MCPEnableTempTokenAuth {
		return identity.ErrUnauthorized
	}
	raw := string(c.Request.Header.Peek("X-Bifrost-Temp-Token"))
	if raw == "" {
		return identity.ErrUnauthorized
	}
	v, err := a.host.TempTokens.Validate(c, raw, string(c.Method()), string(c.Path()))
	if err != nil {
		if errors.Is(err, temptoken.ErrTokenNotFound) || errors.Is(err, temptoken.ErrTokenExpired) || errors.Is(err, temptoken.ErrScopeUnknown) || errors.Is(err, temptoken.ErrRouteNotAllowed) {
			return identity.ErrUnauthorized
		}
		return identity.ErrUnavailable
	}
	if v == nil {
		return identity.ErrUnauthorized
	}
	c.RemoveUserValue(principalKey{})
	c.RemoveUserValue(schemas.IsLocalAdminContextKey)
	c.SetUserValue(schemas.BifrostContextKeyTempTokenScope, v.Scope)
	c.SetUserValue(schemas.BifrostContextKeyTempTokenResourceID, v.ResourceID)
	return nil
}

// APIMiddleware 是全部管理路由的鉴权：只有正常的主管理员会话放行，并为通知等上游能力设置 localAdmin 兼容标记。
// 身份路由与公开入口直接放行；WebSocket 握手用票据或 Cookie 并绑定重验回调；/api/config 做认证投影与预检。
func (a *AuthAdapter) APIMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			c.RemoveUserValue(schemas.IsLocalAdminContextKey)
			c.RemoveUserValue(schemas.BifrostContextKeyUserRoleID)
			c.RemoveUserValue(principalKey{})
			c.RemoveUserValue(handlers.WebSocketAuthorizeContextKey)
			method, path := string(c.Method()), string(c.Path())
			if public(method, path) || identityhttp.OwnsRoute(method, path) {
				next(c)
				return
			}
			ctx := identityhttp.OperationContext(c, "console.authenticate")
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				identityhttp.Error(c, identity.ErrUnauthorized)
				return
			}
			var p identity.Principal
			var err error
			if path == "/ws" {
				// 握手比普通 POST 更严：浏览器发起 WebSocket 必然带 Origin，因此只接受精确 Origin，
				// 不像 SameOrigin 那样接受同源 Referer 兜底（方案 4.5）。
				if string(c.Request.Header.Peek("Origin")) != a.http.Origin() {
					identityhttp.Error(c, identity.ErrForbidden)
					return
				}
				if c.QueryArgs().Has("token") {
					identityhttp.Error(c, identity.ErrUnauthorized)
					return
				}
				if ticket := string(c.QueryArgs().Peek("ticket")); ticket != "" {
					p, err = a.service.ConsumeTicket(ctx, ticket)
				} else {
					p, err = a.service.Authenticate(ctx, string(c.Request.Header.Cookie(identityhttp.CookieName)))
				}
			} else {
				p, err = a.service.Authenticate(ctx, string(c.Request.Header.Cookie(identityhttp.CookieName)))
			}
			if err == nil {
				err = a.service.RequireAccountManager(ctx, p)
			}
			if err != nil {
				if temporaryRoute(method, path) && len(c.Request.Header.Peek("X-Bifrost-Temp-Token")) != 0 {
					if err = a.authorizeTemporary(c); err == nil {
						next(c)
						return
					}
				}
				identityhttp.Error(c, err)
				return
			}
			if method != fasthttp.MethodGet && method != fasthttp.MethodHead && method != fasthttp.MethodOptions && !a.http.SameOrigin(c) {
				identityhttp.Error(c, identity.ErrForbidden)
				return
			}
			c.SetUserValue(principalKey{}, p)
			c.SetUserValue(schemas.IsLocalAdminContextKey, true)
			if path == "/ws" {
				sessionID := p.SessionID // 只捕获会话 ID，不保留 RequestCtx
				c.SetUserValue(handlers.WebSocketAuthorizeContextKey, func(ctx context.Context) error {
					ctx = identity.WithDiagnosticOperation(ctx, "identity.websocket")
					p, err := a.service.ValidateSession(ctx, sessionID)
					if err != nil {
						return err
					}
					return a.service.RequireAccountManager(ctx, p)
				})
			}
			if path == "/api/config" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) {
				a.serveConfig(ctx, c, p, next)
				return
			}
			next(c)
		}
	}
}

var _ server.ConsoleAuthProvider = (*AuthAdapter)(nil)
