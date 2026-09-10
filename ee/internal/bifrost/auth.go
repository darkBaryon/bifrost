// Package bifrost 连接EE身份服务与宿主路由，不实现账号业务或推理鉴权。
package bifrost

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
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

// AuthAdapter 仅持有已装配依赖；业务状态来自identity服务。
type AuthAdapter struct {
	HTTP *identityhttp.Handler
	host *server.BifrostHTTPServer
}

func OptionsFromEnvironment(setupToken string) (identity.Options, error) {
	initial, exists := os.LookupEnv("EE_INITIAL_PASSWORD")
	if !exists {
		initial = "123456"
	}
	hours := 24
	if raw, exists := os.LookupEnv("EE_SESSION_TTL_HOURS"); exists {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > 168 {
			return identity.Options{}, identity.ErrInvalid
		}
		hours = n
	}
	return identity.Options{InitialPassword: initial, SetupToken: setupToken, SessionTTL: time.Duration(hours) * time.Hour}, nil
}

// NewAuthAdapter 在宿主注册路由前迁移并装配身份；无DB或非法部署配置时拒绝启动。
func NewAuthAdapter(ctx context.Context, s *server.BifrostHTTPServer) (*AuthAdapter, error) {
	ctx = identity.WithDiagnosticOperation(ctx, "identity.bootstrap")
	if s.Config == nil || s.Config.ConfigStore == nil || s.Config.ConfigStore.DB() == nil {
		return nil, errors.New("EE identity requires a database config store")
	}
	options, e := OptionsFromEnvironment(s.Config.SetupToken)
	if e != nil {
		return nil, errors.New("invalid EE authentication settings")
	}
	origin := os.Getenv("EE_PUBLIC_ORIGIN")
	if origin == "" {
		ip := net.ParseIP(s.Host)
		if s.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, errors.New("EE_PUBLIC_ORIGIN is required for a non-loopback listener")
		}
		origin = "http://" + net.JoinHostPort(s.Host, s.Port)
	}
	// 先验证部署规则，再创建任何身份数据。
	if _, e = identityhttp.NewHandler(nil, origin); e != nil {
		return nil, errors.New("EE_PUBLIC_ORIGIN must be an HTTPS origin (HTTP only on loopback)")
	}
	if e = s.Config.ConfigStore.RunMigration(ctx, persistence.MigrateIdentity); e != nil {
		return nil, errors.New("EE identity migration failed")
	}
	svc, e := identity.NewService(persistence.NewStore(s.Config.ConfigStore.DB()), persistence.Passwords{}, options, nil)
	if e != nil {
		return nil, errors.New("invalid EE password/session settings")
	}
	state, e := svc.State(ctx)
	if e != nil {
		return nil, e
	}
	if !state.Initialized {
		if e = validateLegacyFile(server.GetDefaultConfigDir(s.AppDir)); e != nil {
			return nil, e
		}
		old, e := s.Config.ConfigStore.GetAuthConfig(ctx)
		if e != nil {
			return nil, identity.ErrUnavailable
		}
		effective := old
		if s.Config.GovernanceConfig != nil && s.Config.GovernanceConfig.AuthConfig != nil {
			effective = s.Config.GovernanceConfig.AuthConfig
		}
		if effective != nil {
			if effective.AdminUserName == nil || effective.AdminPassword == nil || effective.AdminUserName.GetValue() == "" || effective.AdminPassword.GetValue() == "" {
				return nil, errors.New("legacy administrator credentials are incomplete")
			}
			// 只有宿主已解析的完整bcrypt快照才可作为迁移输入。
			c := identity.Credential{Account: identity.Account{Username: effective.AdminUserName.GetValue()}, PasswordHash: effective.AdminPassword.GetValue()}
			if e = svc.BootstrapLegacy(ctx, &c); e != nil {
				return nil, errors.New("legacy administrator password is not a supported bcrypt hash")
			}
			if old != nil && old.AdminUserName != nil && old.AdminPassword != nil && old.AdminUserName.GetValue() == c.Username && old.AdminPassword.GetValue() == c.PasswordHash {
				log.Print("EE identity: imported resolved host administrator snapshot matching stored database credentials")
			} else {
				log.Print("EE identity: imported resolved host administrator snapshot; stored legacy credentials differ")
			}
		}
	}
	h, e := identityhttp.NewHandler(svc, origin)
	if e != nil {
		return nil, e
	}
	return &AuthAdapter{HTTP: h, host: s}, nil
}

// 宿主会跳过残缺文件凭据，届时effective可能为nil。先辨别显式坏输入，
// 防止把这种实例当作允许初始化的空实例；密码哈希与来源选择仍归宿主。
func validateLegacyFile(appDir string) error {
	b, e := os.ReadFile(filepath.Join(appDir, "config.json"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
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
	if a != nil && (a.AdminUserName == nil || a.AdminPassword == nil || a.AdminUserName.GetValue() == "" || a.AdminPassword.GetValue() == "") {
		return errors.New("legacy administrator credentials are incomplete or unresolved")
	}
	return nil
}
func (a *AuthAdapter) RegisterSessionRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	a.HTTP.RegisterRoutes(r, m...)
}
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
func temporaryRoute(method, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 5 {
		return false
	}
	if parts[0] != "api" {
		return false
	}
	if parts[1] == "oauth" && parts[2] == "per-user" && parts[3] == "flows" && parts[4] != "" {
		return method == fasthttp.MethodGet && (len(parts) == 5 || (len(parts) == 6 && parts[5] == "start"))
	}
	return len(parts) == 5 && parts[4] != "" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) && ((parts[1] == "mcp" && parts[2] == "per-user-headers" && parts[3] == "flows") || (parts[1] == "oauth2" && parts[2] == "consent" && parts[3] == "flows"))
}
func (a *AuthAdapter) authorizeTemporary(c *fasthttp.RequestCtx) error {
	if a.host == nil || a.host.TempTokens == nil {
		return identity.ErrUnauthorized
	}
	config, e := a.host.Config.ConfigStore.GetClientConfig(c)
	if e != nil {
		return identity.ErrUnavailable
	}
	if config == nil || !config.MCPEnableTempTokenAuth {
		return identity.ErrUnauthorized
	}
	raw := string(c.Request.Header.Peek("X-Bifrost-Temp-Token"))
	if raw == "" {
		return identity.ErrUnauthorized
	}
	v, e := a.host.TempTokens.Validate(c, raw, string(c.Method()), string(c.Path()))
	if e != nil {
		if errors.Is(e, temptoken.ErrTokenNotFound) || errors.Is(e, temptoken.ErrTokenExpired) || errors.Is(e, temptoken.ErrScopeUnknown) || errors.Is(e, temptoken.ErrRouteNotAllowed) {
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
func (a *AuthAdapter) APIMiddleware() schemas.BifrostHTTPMiddleware {
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			c.RemoveUserValue(schemas.IsLocalAdminContextKey)
			c.RemoveUserValue(schemas.BifrostContextKeyUserRoleID)
			c.RemoveUserValue(principalKey{})
			c.RemoveUserValue(handlers.WebSocketAuthorizeContextKey)
			method, path := string(c.Method()), string(c.Path())
			ctx := identityhttp.OperationContext(c, "console.authenticate")
			wsTicket := string(c.QueryArgs().Peek("ticket"))
			legacyWSToken := c.QueryArgs().Has("token")
			if public(method, path) || identityhttp.OwnsRoute(method, path) {
				next(c)
				return
			}
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				identityhttp.Error(c, identity.ErrUnauthorized)
				return
			}
			var p identity.Principal
			var e error
			if path == "/ws" {
				if string(c.Request.Header.Peek("Origin")) != a.HTTP.Origin {
					identityhttp.Error(c, identity.ErrForbidden)
					return
				}
				if legacyWSToken {
					identityhttp.Error(c, identity.ErrUnauthorized)
					return
				}
				if wsTicket != "" {
					p, e = a.HTTP.Service.ConsumeTicket(ctx, wsTicket)
				} else {
					p, e = a.HTTP.Service.Authenticate(ctx, string(c.Request.Header.Cookie(identityhttp.CookieName)))
				}
			} else {
				p, e = a.HTTP.Service.Authenticate(ctx, string(c.Request.Header.Cookie(identityhttp.CookieName)))
			}
			if e == nil {
				e = a.HTTP.Service.RequireAccountManager(ctx, p)
			}
			if e != nil {
				if temporaryRoute(method, path) && len(c.Request.Header.Peek("X-Bifrost-Temp-Token")) != 0 {
					e = a.authorizeTemporary(c)
					if e == nil {
						next(c)
						return
					}
				}
				identityhttp.Error(c, e)
				return
			}
			if method != fasthttp.MethodGet && method != fasthttp.MethodHead && method != fasthttp.MethodOptions && !a.HTTP.SameOrigin(c) {
				identityhttp.Error(c, identity.ErrForbidden)
				return
			}
			c.SetUserValue(principalKey{}, p)
			c.SetUserValue(schemas.IsLocalAdminContextKey, true)
			if path == "/ws" {
				sessionID := p.SessionID
				c.SetUserValue(handlers.WebSocketAuthorizeContextKey, func(ctx context.Context) error {
					ctx = identity.WithDiagnosticOperation(ctx, "identity.websocket")
					p, e := a.HTTP.Service.ValidateSession(ctx, sessionID)
					if e != nil {
						return e
					}
					return a.HTTP.Service.RequireAccountManager(ctx, p)
				})
			}
			if path == "/api/config" && (method == fasthttp.MethodGet || method == fasthttp.MethodPut) {
				projection, e := a.projection(ctx, p)
				if e != nil {
					identityhttp.Error(c, e)
					return
				}
				if method == fasthttp.MethodPut {
					if e = a.checkConfig(c, projection); e != nil {
						identityhttp.Error(c, e)
						return
					}
				}
				next(c)
				if method == fasthttp.MethodGet && c.Response.StatusCode() == fasthttp.StatusOK {
					body, e := sjson.SetBytes(c.Response.Body(), "auth_config", projection)
					if e == nil {
						body, e = sjson.SetBytes(body, "client_config.whitelisted_routes", []string{})
					}
					if e != nil {
						identityhttp.Error(c, identity.ErrUnavailable)
						return
					}
					c.Response.SetBody(body)
				}
				return
			}
			next(c)
		}
	}
}
func (a *AuthAdapter) projection(ctx context.Context, p identity.Principal) (map[string]any, error) {
	account, e := a.HTTP.Service.Me(ctx, p)
	if e != nil {
		return nil, e
	}
	return map[string]any{"is_enabled": true, "admin_username": schemas.NewSecretVar(account.Username), "admin_password": schemas.NewSecretVar("<redacted>")}, nil
}
func (a *AuthAdapter) checkConfig(c *fasthttp.RequestCtx, projection map[string]any) error {
	body := c.PostBody()
	// 宿主JSON字段不区分大小写；拒绝安全字段的大小写别名，保持预检与实际解析一致。
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
	if e := identityhttp.ValidateJSONObject(body); e != nil {
		return e
	}
	if v := gjson.GetBytes(body, "auth_config"); v.Exists() {
		var provided, expected any
		b, e := json.Marshal(projection)
		if e != nil {
			return identity.ErrUnavailable
		}
		if json.Unmarshal([]byte(v.Raw), &provided) != nil || json.Unmarshal(b, &expected) != nil || !reflect.DeepEqual(provided, expected) {
			return identity.ErrConflict
		}
	}
	if v := gjson.GetBytes(body, "client_config.whitelisted_routes"); v.Exists() && (!v.IsArray() || len(v.Array()) != 0) {
		return identity.ErrConflict
	}
	b, e := sjson.DeleteBytes(body, "auth_config")
	if e != nil {
		return identity.ErrInvalid
	}
	c.Request.SetBody(b)
	return nil
}

var _ server.ConsoleAuthProvider = (*AuthAdapter)(nil)
