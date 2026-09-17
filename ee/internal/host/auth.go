// Package host 把EE的登录检查接到Bifrost已有接口上，并处理旧管理员导入、配置和WebSocket连接。
// 账号的创建、密码和角色规则由业务模块负责。
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

// consoleSessions 列出Bifrost接口所需的登录检查和账号查询，由身份模块的SessionService提供。
type consoleSessions interface {
	Authenticate(context.Context, string) (identity.Principal, error)
	ValidateSession(context.Context, string) (identity.Principal, error)
	RequireAccountManager(context.Context, identity.Principal) error
	ConsumeTicket(context.Context, string) (identity.Principal, error)
	Me(context.Context, identity.Principal) (identity.Account, error)
}

// legacyImporter 先查询系统是否已初始化，再按需导入旧管理员，由身份模块的AccountService提供。
type legacyImporter interface {
	State(context.Context) (identity.State, error)
	BootstrapLegacy(ctx context.Context, username, passwordHash string) error
}

// identityRoutes 用于注册身份接口，以及查询和检查控制台的访问来源，由身份HTTP处理器提供。
type identityRoutes interface {
	Origin() string
	SameOrigin(*fasthttp.RequestCtx) bool
	RegisterRoutes(*router.Router, ...schemas.BifrostHTTPMiddleware)
}

// ConsoleAccess 检查已有管理接口的角色权限，并返回本次请求需要执行的前后处理。
type ConsoleAccess interface {
	Prepare(context.Context, *fasthttp.RequestCtx, identity.Principal) (schemas.BifrostHTTPMiddleware, error)
}

// WithConsoleAccess 接入角色权限组件；已接入接口检查失败时直接拒绝，不回退管理员检查。
func WithConsoleAccess(access ConsoleAccess) AuthOption {
	return func(a *AuthAdapter) { a.access = access }
}

// AdditionalRoutes 让角色等模块注册自己的接口，并告诉Bifrost哪些请求由该模块处理。
// 匹配到的请求交给模块自己检查登录和权限。
type AdditionalRoutes interface {
	RegisterRoutes(*router.Router, ...schemas.BifrostHTTPMiddleware)
	OwnsRoute(string, string) bool
}

// AuthOption 用于创建AuthAdapter时传入额外配置，例如角色接口或管理员查询。
type AuthOption func(*AuthAdapter)

// WithAdditionalRoutes 将角色等模块的接口加入同一个Bifrost应用。
func WithAdditionalRoutes(routes AdditionalRoutes) AuthOption {
	return func(a *AuthAdapter) { a.additional = routes }
}

// WithRecoveryAnchor 传入一个查询函数，用来查系统初始化时指定的管理员。
// 这个账号用于离线恢复，也用于限制尚未接入角色权限的Bifrost管理接口；查询不返回密码。
func WithRecoveryAnchor(lookup func(context.Context) (identity.Account, error)) AuthOption {
	return func(a *AuthAdapter) { a.anchor = lookup }
}

// AuthAdapter 将Bifrost的管理请求交给EE检查登录和权限；所需服务由app创建后传入。
type AuthAdapter struct {
	service    consoleSessions
	http       identityRoutes
	host       *server.BifrostHTTPServer
	access     ConsoleAccess
	additional AdditionalRoutes
	anchor     func(context.Context) (identity.Account, error)
}

// NewAuthAdapter 接收身份模块的登录检查、HTTP处理器和可选配置，供Bifrost注册和保护管理接口。
func NewAuthAdapter(host *server.BifrostHTTPServer, sessions consoleSessions, handler identityRoutes, options ...AuthOption) *AuthAdapter {
	a := &AuthAdapter{service: sessions, http: handler, host: host}
	for _, option := range options {
		option(a)
	}
	return a
}

// Logger 用于记录旧管理员导入结果，由app传入；msg可用%s等占位符。
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

// RegisterSessionRoutes 注册EE身份接口，替换Bifrost旧会话接口；传入角色接口时也一起注册。
func (a *AuthAdapter) RegisterSessionRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	a.http.RegisterRoutes(r, m...)
	if a.additional != nil {
		a.additional.RegisterRoutes(r, m...)
	}
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

// requireHostAdministrator 检查操作者是否为系统初始化时指定的管理员；调用前必须已验证登录。
// 尚未接入角色权限的Bifrost管理接口仍只允许这个账号访问，Users.Manage只允许管理账号和角色。
// 未传入管理员查询函数时，沿用身份模块的管理员检查。
func (a *AuthAdapter) requireHostAdministrator(ctx context.Context, p identity.Principal) error {
	if a.anchor == nil {
		return a.service.RequireAccountManager(ctx, p)
	}
	if p.MustChangePassword {
		return identity.ErrForbidden
	}
	account, err := a.anchor(ctx)
	if err != nil {
		return identity.SafeError(err)
	}
	if p.AccountID != account.ID {
		return identity.ErrForbidden
	}
	return nil
}

// APIMiddleware 在Bifrost处理管理请求前检查登录；身份和角色接口交给各自模块检查，公开接口直接放行。
// 已接入的管理接口使用角色权限，其余仍要求系统初始化时指定的管理员；原有临时令牌入口按原规则验证。
// WebSocket连接会再次检查登录，配置接口还会限制旧认证字段的读写。
func (a *AuthAdapter) APIMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			c.RemoveUserValue(schemas.IsLocalAdminContextKey)
			c.RemoveUserValue(schemas.BifrostContextKeyUserRoleID)
			c.RemoveUserValue(principalKey{})
			c.RemoveUserValue(handlers.WebSocketAuthorizeContextKey)
			method, path := string(c.Method()), string(c.Path())
			if public(method, path) || identityhttp.OwnsRoute(method, path) || (a.additional != nil && a.additional.OwnsRoute(method, path)) {
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
			var wrapper schemas.BifrostHTTPMiddleware
			if err == nil {
				if a.access != nil {
					wrapper, err = a.access.Prepare(ctx, c, p)
					if err == nil && wrapper == nil {
						err = identity.ErrUnavailable
					}
				} else {
					err = a.requireHostAdministrator(ctx, p)
				}
			}
			if err != nil {
				if (errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden)) && temporaryRoute(method, path) && len(c.Request.Header.Peek("X-Bifrost-Temp-Token")) != 0 {
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
			if wrapper == nil {
				c.SetUserValue(schemas.IsLocalAdminContextKey, true)
			}
			if path == "/ws" && wrapper == nil {
				sessionID := p.SessionID // 连接后还会检查登录，只保留会话编号，避免继续引用已结束的HTTP请求。
				c.SetUserValue(handlers.WebSocketAuthorizeContextKey, func(ctx context.Context) error {
					ctx = identity.WithDiagnosticOperation(ctx, "identity.websocket")
					p, err := a.service.ValidateSession(ctx, sessionID)
					if err != nil {
						return err
					}
					return a.requireHostAdministrator(ctx, p)
				})
			}
			if path == "/api/config" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) {
				configHandler := next
				if wrapper != nil {
					configHandler = wrapper(next)
				}
				a.serveConfig(ctx, c, p, configHandler)
				return
			}
			if wrapper != nil {
				wrapper(next)(c)
			} else {
				next(c)
			}
		}
	}
}

var _ server.ConsoleAuthProvider = (*AuthAdapter)(nil)
