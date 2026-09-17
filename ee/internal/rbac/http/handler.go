// Package rbachttp 接收角色和权限的HTTP请求，检查登录后调用rbac业务服务，再返回JSON结果。
// 本文件集中放接口地址、路由注册，以及所有接口共用的来源和登录检查。
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

// Sessions 用Cookie里的登录凭据核实当前登录状态，并查出是谁在操作。
type Sessions interface {
	Authenticate(context.Context, string) (identity.Principal, error)
}

// OriginChecker 检查请求是否来自配置的控制台地址；具体规则复用身份接口的实现。
type OriginChecker interface {
	SameOrigin(*fasthttp.RequestCtx) bool
}

// Handler 持有角色服务、登录检查和来源检查，供下面的九个接口共用。
type Handler struct {
	service  *rbac.Service
	sessions Sessions
	origin   OriginChecker
}

// NewHandler 接收角色服务、登录检查和来源检查；缺少依赖时请求会返回503。
func NewHandler(service *rbac.Service, sessions Sessions, origin OriginChecker) *Handler {
	return &Handler{service: service, sessions: sessions, origin: origin}
}

// request 保存通过登录检查的请求，供具体接口读取参数和调用业务服务。
type request struct {
	ctx     context.Context
	http    *fasthttp.RequestCtx
	subject rbac.Subject
}

// endpoint 是具体接口的处理函数，返回HTTP状态、响应内容或错误。
type endpoint func(*Handler, request) (int, any, error)

// route 将一个接口地址与它的处理函数放在一起。
type route struct {
	path   string
	handle endpoint
}

var routes = []route{
	{"/api/permissions/list", (*Handler).listPermissions}, // 可分配的权限目录
	{"/api/permissions/me", (*Handler).myPermissions},     // 当前账号的角色和权限

	{"/api/roles/list", (*Handler).listRoles},    // 角色列表
	{"/api/roles/get", (*Handler).getRole},       // 单个角色详情
	{"/api/roles/create", (*Handler).createRole}, // 创建角色
	{"/api/roles/update", (*Handler).updateRole}, // 修改角色
	{"/api/roles/delete", (*Handler).deleteRole}, // 删除角色

	{"/api/accounts/get-roles", (*Handler).getAccountRoles}, // 查询账号的角色
	{"/api/accounts/set-roles", (*Handler).setAccountRoles}, // 替换账号的角色
}

// RegisterRoutes 把九个接口注册到Bifrost路由中，每个接口都先经过传入的公共中间件。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range routes {
		r.POST(rt.path, lib.ChainMiddlewares(h.serve(rt), m...))
	}
}

// serve 是所有接口共用的处理流程：检查来源、验证登录、调用接口、写回响应。
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
		// 这些控制台接口只接受登录Cookie；带Authorization头的请求直接拒绝。
		if len(c.Request.Header.Peek("Authorization")) > 0 {
			Error(c, rbac.ErrUnauthorized)
			return
		}
		ctx := authhttp.OperationContext(c, "rbac"+rt.path)
		principal, err := h.sessions.Authenticate(ctx, string(c.Request.Header.Cookie(authhttp.CookieName)))
		if err != nil {
			authhttp.Error(c, err)
			return
		}
		status, body, err := rt.handle(h, request{ctx: ctx, http: c, subject: policy.Subject(principal)})
		if err != nil {
			Error(c, err)
			return
		}
		authhttp.JSON(c, status, body)
	}
}

// OwnsRoute 按请求方法和完整路径判断是不是本包的接口；只认路由表中列出的POST地址。
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

// OwnsRoute 供Bifrost识别本包的接口；匹配后交给本包检查登录，再由rbac服务检查操作权限。
func (h *Handler) OwnsRoute(method, path string) bool { return OwnsRoute(method, path) }
