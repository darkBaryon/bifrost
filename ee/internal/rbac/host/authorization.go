// Package host 为Bifrost已有接口检查角色权限，并隐藏当前账号不能读取的内容。
// 本文件提供请求处理入口；登录身份由现有host认证适配器传入。
package host

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// Adapter 持有角色服务、路由匹配器和日志；每次请求重新读取权限，不缓存账号的授权结果。
type Adapter struct {
	service *rbac.Service
	matcher *router.Router
	log     Logger
	config  *lib.Config
}

// NewAdapter 创建权限接入组件；接收请求前必须先调用VerifyRoutes核对路由。
func NewAdapter(service *rbac.Service, config *lib.Config, log Logger) *Adapter {
	return &Adapter{service: service, config: config, log: log}
}

// requestAccess 保存当前操作者、这次查到的权限和命中的接口规则，只用于当前请求。
type requestAccess struct {
	Subject rbac.Subject
	Access  rbac.Access
	Route   routeEntry
}

// Prepare 重新检查登录和本接口所需权限；通过后返回本次请求使用的中间件。
// 中间件先检查敏感操作，再运行原handler，最后隐藏不能返回的字段。
func (a *Adapter) Prepare(ctx context.Context, c *fasthttp.RequestCtx, p identity.Principal) (schemas.BifrostHTTPMiddleware, error) {
	if a.service == nil {
		return nil, identity.ErrUnavailable
	}
	entry, ok := a.match(string(c.Method()), string(c.Path()))
	if !ok || entry.Kind != kindManaged {
		return nil, identity.ErrForbidden
	}
	subject := policy.Subject(p)
	access, e := a.service.Snapshot(ctx, subject)
	if e != nil {
		return nil, policy.IdentityError(e)
	}
	if e := requirePermissions(access, entry.Permissions...); e != nil {
		return nil, policy.IdentityError(e)
	}
	request := requestAccess{Subject: subject, Access: access, Route: entry}
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(c *fasthttp.RequestCtx) {
			if e := a.checkSensitiveRequest(c, request); e != nil {
				rbachttp.Error(c, e)
				return
			}
			a.installPolicies(c, request)
			next(c)
			if e := projectSensitiveResponse(c, request); e != nil {
				a.diagnostic(ctx, request.Route.Pattern, "invalid_projection")
				resetKeepingRequestID(c)
				rbachttp.Error(c, e)
			}
		}
	}, nil
}

// Logger 用于记录权限处理失败的位置，由app传入；不记录密钥或响应正文。
type Logger interface{ Warn(string, ...any) }

// diagnostic 只记录失败位置和关联编号，方便从服务端日志定位问题。
func (a *Adapter) diagnostic(ctx context.Context, route, reason string) {
	if a.log == nil {
		return
	}
	operation, id := identity.DiagnosticOperation(ctx)
	a.log.Warn("EE RBAC rejection operation=%s route=%s reason=%s incident=%s", operation, route, reason, id)
}

// resetKeepingRequestID 清除原响应中的内容和头，只保留请求编号，避免错误信息夹带敏感数据。
func resetKeepingRequestID(c *fasthttp.RequestCtx) {
	requestID := string(c.Response.Header.Peek("X-Request-ID"))
	c.Response.Reset()
	c.Response.Header.Set("X-Request-ID", requestID)
}
