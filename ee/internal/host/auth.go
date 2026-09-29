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

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/auth/http"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/temptoken"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/valyala/fasthttp"
)

type principalKey struct{}

// consoleAuthentication 提供宿主所需的认证读取；权限和票据分别通过独立依赖调用。
type consoleAuthentication interface {
	Authenticate(context.Context, string) (session.Principal, error)
	Me(context.Context, session.Principal) (auth.User, error)
}

// ticketConsumer 在当前业务事务内消费握手票据并复验身份及通知权限。
type ticketConsumer interface {
	ConsumeTicket(context.Context, string) (session.Principal, error)
}

// legacyImporter 先查询系统是否已初始化，再按需导入旧管理员，由身份模块的AccountService提供。
type legacyImporter interface {
	State(context.Context) (identity.State, error)
	BootstrapLegacy(ctx context.Context, username, passwordHash string) error
}

// consoleOrigin 查询和检查控制台的访问来源，由 auth/http 的浏览器策略提供。
type consoleOrigin interface {
	Origin() string
	SameOrigin(*fasthttp.RequestCtx) bool
}

// ConsoleAccess 检查已有管理接口的角色权限，并返回本次请求需要执行的前后处理。
type ConsoleAccess interface {
	Prepare(context.Context, *fasthttp.RequestCtx, session.Principal) (schemas.BifrostHTTPMiddleware, error)
}

// AuthOption 用于创建AuthAdapter时传入额外配置，例如管理接口授权或管理员查询。
type AuthOption func(*AuthAdapter)

// WithRecoveryAnchor 传入一个查询函数，用来查系统初始化时指定的管理员。
// 供/api/config返回兼容认证信息；查询不返回密码，也不参与管理接口授权。
func WithRecoveryAnchor(lookup func(context.Context) (identity.Account, error)) AuthOption {
	return func(a *AuthAdapter) { a.anchor = lookup }
}

// AuthAdapter 将Bifrost的管理请求交给EE检查登录和权限；所需服务由app创建后传入。
type AuthAdapter struct {
	authentication consoleAuthentication
	tickets        ticketConsumer
	http           consoleOrigin
	host           *server.BifrostHTTPServer
	access         ConsoleAccess
	anchor         func(context.Context) (identity.Account, error)
	consoleLog     ConsoleLogger
}

// NewAuthAdapter 分别接收认证、票据消费、控制台来源和管理权限；模块接口由app分别注册。
// 缺少任一必要依赖时，受保护请求返回不可用，不回退旧管理员策略。
func NewAuthAdapter(host *server.BifrostHTTPServer, authentication consoleAuthentication, tickets ticketConsumer, origin consoleOrigin, access ConsoleAccess, options ...AuthOption) *AuthAdapter {
	a := &AuthAdapter{authentication: authentication, tickets: tickets, http: origin, host: host, access: access}
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

// APIMiddleware 在Bifrost处理管理请求前检查登录，公开接口直接放行。
// 身份、角色和基础状态接口由app单独注册，不接入此中间件。
// 管理权限由必需的角色组件检查；原有临时令牌入口保持原规则。
// WebSocket连接会再次检查登录，配置接口还会限制旧认证字段的读写。
func (a *AuthAdapter) APIMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			c.RemoveUserValue(schemas.IsLocalAdminContextKey)
			c.RemoveUserValue(schemas.BifrostContextKeyUserRoleID)
			c.RemoveUserValue(principalKey{})
			c.RemoveUserValue(handlers.WebSocketAuthorizeContextKey)
			method, path := string(c.Method()), string(c.Path())
			if public(method, path) {
				next(c)
				return
			}
			if a.authentication == nil || a.tickets == nil || a.access == nil || a.http == nil {
				writeConsoleError(c, identity.ErrUnavailable)
				return
			}
			ctx := httpx.OperationContext(c, "console.authenticate")
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				writeConsoleError(c, identity.ErrUnauthorized)
				return
			}
			var p session.Principal
			var err error
			if path == "/ws" {
				// 握手比普通 POST 更严：浏览器发起 WebSocket 必然带 Origin，因此只接受精确 Origin，
				// 不像 SameOrigin 那样接受同源 Referer 兜底（方案 4.5）。
				if string(c.Request.Header.Peek("Origin")) != a.http.Origin() {
					writeConsoleError(c, identity.ErrForbidden)
					return
				}
				if c.QueryArgs().Has("token") {
					writeConsoleError(c, identity.ErrUnauthorized)
					return
				}
				if ticket := string(c.QueryArgs().Peek("ticket")); ticket != "" {
					p, err = a.tickets.ConsumeTicket(ctx, ticket)
				} else {
					p, err = a.authentication.Authenticate(ctx, string(c.Request.Header.Cookie(authhttp.CookieName)))
				}
			} else {
				p, err = a.authentication.Authenticate(ctx, string(c.Request.Header.Cookie(authhttp.CookieName)))
			}
			var wrapper schemas.BifrostHTTPMiddleware
			if err == nil {
				wrapper, err = a.access.Prepare(ctx, c, p)
				if err == nil && wrapper == nil {
					err = identity.ErrUnavailable
				}
			}
			if err != nil {
				if temporaryFallbackError(err) && temporaryRoute(method, path) && len(c.Request.Header.Peek("X-Bifrost-Temp-Token")) != 0 {
					if err = a.authorizeTemporary(c); err == nil {
						next(c)
						return
					}
				}
				writeConsoleError(c, err)
				return
			}
			if method != fasthttp.MethodGet && method != fasthttp.MethodHead && method != fasthttp.MethodOptions && !a.http.SameOrigin(c) {
				writeConsoleError(c, identity.ErrForbidden)
				return
			}
			c.SetUserValue(principalKey{}, p)
			if path == "/api/config" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) {
				a.serveConfig(ctx, c, p, wrapper(next))
				return
			}
			wrapper(next)(c)
		}
	}
}

func temporaryFallbackError(err error) bool {
	return errors.Is(err, identity.ErrUnauthorized) || errors.Is(err, identity.ErrForbidden) ||
		errors.Is(err, auth.ErrUnauthorized) || errors.Is(err, auth.ErrForbidden)
}

// writeConsoleError 同时接收旧账号接入和新认证服务的错误，保留既有客户端错误码。
func writeConsoleError(c *fasthttp.RequestCtx, err error) {
	var accountError identity.Error
	if errors.As(err, &accountError) {
		err = auth.Error(accountError)
	}
	authhttp.Error(c, err)
}
