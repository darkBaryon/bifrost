// Package rbachttp 提供角色及权限接口，复用身份认证与同源规则。
package rbachttp

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// Sessions 是权限接口需要的认证能力。
type Sessions interface {
	Authenticate(context.Context, string) (identity.Principal, error)
}

// OriginChecker 复用身份模块已经验证的部署来源规则。
type OriginChecker interface {
	SameOrigin(*fasthttp.RequestCtx) bool
}

// Handler 只持有服务与两个窄能力，不持有宿主Server或数据库。
type Handler struct {
	service  *rbac.Service
	sessions Sessions
	origin   OriginChecker
}

// NewHandler 构造权限接口；依赖缺失不能默认放行。
func NewHandler(service *rbac.Service, sessions Sessions, origin OriginChecker) *Handler {
	return &Handler{service: service, sessions: sessions, origin: origin}
}

type request struct {
	ctx context.Context
	c   *fasthttp.RequestCtx
	p   rbac.Subject
}

type endpoint func(*Handler, request) (int, any, error)

type route struct {
	path   string
	handle endpoint
}

var routes = []route{
	{"/api/permissions/list", (*Handler).permissions}, // 可分配的权限目录
	{"/api/permissions/me", (*Handler).me},            // 当前账号的角色和权限
	{"/api/roles/list", (*Handler).list},              // 角色列表
	{"/api/roles/get", (*Handler).get},                // 单个角色详情
	{"/api/roles/create", (*Handler).create},          // 创建角色
	{"/api/roles/update", (*Handler).update},          // 修改角色
	{"/api/roles/delete", (*Handler).delete},          // 删除角色
	{"/api/accounts/get-roles", (*Handler).getRoles},  // 查询账号的角色
	{"/api/accounts/set-roles", (*Handler).setRoles},  // 替换账号的角色
}

// OwnsRoute 精确识别自管端点，服务内部重新判权。
func OwnsRoute(method, path string) bool {
	return method == fasthttp.MethodPost && ownsPath(path)
}

func ownsPath(path string) bool {
	for _, r := range routes {
		if r.path == path {
			return true
		}
	}
	return false
}

// GuardMethods 让角色接口的错误请求方法返回405，避免落到宿主的前端页面兜底路由。
func GuardMethods(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		if !c.IsPost() && ownsPath(string(c.Path())) {
			c.Response.Header.Set("Cache-Control", "no-store")
			c.Response.Header.Set("Allow", fasthttp.MethodPost)
			Error(c, FromStatus(fasthttp.StatusMethodNotAllowed))
			return
		}
		next(c)
	}
}

// RegisterRoutes 给所有端点应用宿主公共中间件。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range routes {
		r.POST(rt.path, lib.ChainMiddlewares(h.serve(rt), m...))
	}
}

func (h *Handler) serve(rt route) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		c.Response.Header.Set("Cache-Control", "no-store")
		if h.service == nil || h.sessions == nil || h.origin == nil {
			Error(c, rbac.ErrUnavailable)
			return
		}
		if !h.origin.SameOrigin(c) {
			Error(c, rbac.ErrForbidden)
			return
		}
		if len(c.Request.Header.Peek("Authorization")) > 0 {
			Error(c, rbac.ErrUnauthorized)
			return
		}
		ctx := authhttp.OperationContext(c, "rbac"+rt.path)
		principal, e := h.sessions.Authenticate(ctx, string(c.Request.Header.Cookie(authhttp.CookieName)))
		if e != nil {
			authhttp.Error(c, e)
			return
		}
		status, body, e := rt.handle(h, request{ctx: ctx, c: c, p: policy.Subject(principal)})
		if e != nil {
			Error(c, e)
			return
		}
		authhttp.JSON(c, status, body)
	}
}

// OwnsRoute 提供构造时注入的精确自管路由识别。
func (h *Handler) OwnsRoute(method, path string) bool { return OwnsRoute(method, path) }
