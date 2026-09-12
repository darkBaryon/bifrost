// Package bifrost 连接 EE 身份服务与宿主路由：替换管理鉴权、导入旧管理员、兼容 WS/配置/临时令牌；不实现账号业务。
package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/temptoken"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"
)

type principalKey struct{}

// AuthAdapter 实现宿主的 ConsoleAuthProvider；依赖由 app 装配后注入，宿主对象只用于临时令牌与配置读取。
type AuthAdapter struct {
	service *identity.Service
	http    *identityhttp.Handler
	host    *server.BifrostHTTPServer
}

// NewAuthAdapter 由 app 在身份服务与 HTTP 适配构造完成后调用。
func NewAuthAdapter(host *server.BifrostHTTPServer, service *identity.Service, handler *identityhttp.Handler) *AuthAdapter {
	return &AuthAdapter{service: service, http: handler, host: host}
}

// ImportLegacyAdministrator 在身份未初始化时，把宿主已解析的旧管理员凭据导入为主管理员；已初始化时不读取旧配置。
// 宿主会忽略残缺的文件凭据，所以先直接检查配置文件：残缺或未解析的输入拒绝启动，不能当作空实例开放初始化。
func ImportLegacyAdministrator(ctx context.Context, host *server.BifrostHTTPServer, service *identity.Service) error {
	state, err := service.State(ctx)
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
	if err := service.BootstrapLegacy(ctx, username, hash); err != nil {
		return errors.New("legacy administrator password is not a supported bcrypt hash")
	}
	if storedUser, storedHash := credential(stored); storedUser == username && storedHash == hash {
		log.Print("EE identity: imported resolved host administrator snapshot matching stored database credentials")
	} else {
		log.Print("EE identity: imported resolved host administrator snapshot; stored legacy credentials differ")
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

// serveConfig 对 GET 用只读投影替换响应中的旧认证字段，对 PUT 先预检再剔除认证字段交给宿主。
func (a *AuthAdapter) serveConfig(ctx context.Context, c *fasthttp.RequestCtx, p identity.Principal, next fasthttp.RequestHandler) {
	projection, err := a.projection(ctx, p)
	if err != nil {
		identityhttp.Error(c, err)
		return
	}
	if c.IsPut() {
		if err = a.checkConfig(c, projection); err != nil {
			identityhttp.Error(c, err)
			return
		}
	}
	next(c)
	if c.IsGet() && c.Response.StatusCode() == fasthttp.StatusOK {
		body, err := sjson.SetBytes(c.Response.Body(), "auth_config", projection)
		if err == nil {
			body, err = sjson.SetBytes(body, "client_config.whitelisted_routes", []string{})
		}
		if err != nil {
			identityhttp.Error(c, identity.ErrUnavailable)
			return
		}
		c.Response.SetBody(body)
	}
}

// projection 是旧 auth_config 的只读投影：认证启用、主管理员登录名、脱敏密码。
func (a *AuthAdapter) projection(ctx context.Context, p identity.Principal) (map[string]any, error) {
	account, err := a.service.Me(ctx, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"is_enabled": true, "admin_username": schemas.NewSecretVar(account.Username), "admin_password": schemas.NewSecretVar("<redacted>")}, nil
}

// checkConfig 拒绝对旧认证或免认证白名单的任何实际修改（整请求 409），同值回送则剔除 auth_config 后放行。
// 宿主 JSON 字段不区分大小写，因此安全字段的大小写别名一律 400，保持预检与实际解析一致。
func (a *AuthAdapter) checkConfig(c *fasthttp.RequestCtx, projection map[string]any) error {
	body := c.PostBody()
	var root map[string]json.RawMessage
	if json.Unmarshal(body, &root) != nil {
		return identity.ErrInvalid
	}
	for key := range root {
		if (strings.EqualFold(key, "auth_config") && key != "auth_config") || (strings.EqualFold(key, "client_config") && key != "client_config") {
			return identity.ErrInvalid
		}
	}
	if raw, ok := root["client_config"]; ok {
		var client map[string]json.RawMessage
		if json.Unmarshal(raw, &client) != nil {
			return identity.ErrInvalid
		}
		for key := range client {
			if strings.EqualFold(key, "whitelisted_routes") && key != "whitelisted_routes" {
				return identity.ErrInvalid
			}
		}
	}
	if err := identityhttp.ValidateJSONObject(body); err != nil {
		return err
	}
	if v := gjson.GetBytes(body, "auth_config"); v.Exists() {
		var provided, expected any
		b, err := json.Marshal(projection)
		if err != nil {
			return identity.ErrUnavailable
		}
		if json.Unmarshal([]byte(v.Raw), &provided) != nil || json.Unmarshal(b, &expected) != nil || !reflect.DeepEqual(provided, expected) {
			return identity.ErrConflict
		}
	}
	if v := gjson.GetBytes(body, "client_config.whitelisted_routes"); v.Exists() && (!v.IsArray() || len(v.Array()) != 0) {
		return identity.ErrConflict
	}
	b, err := sjson.DeleteBytes(body, "auth_config")
	if err != nil {
		return identity.ErrInvalid
	}
	c.Request.SetBody(b)
	return nil
}

var _ server.ConsoleAuthProvider = (*AuthAdapter)(nil)
