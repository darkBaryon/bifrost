// Package usagehttp 提供模板管理接口，复用身份协议并在存储事务内判权。
package usagehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/usage"
	store "github.com/darkBaryon/bifrost/ee/internal/usage/persistence"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// Sessions 只读取Cookie对应的登录身份。
type Sessions interface {
	Authenticate(context.Context, string) (identity.Principal, error)
}

// OriginChecker 复用控制台的同源校验。
type OriginChecker interface {
	SameOrigin(*fasthttp.RequestCtx) bool
}

// Handler 只负责HTTP转换；存储负责事务内的实时授权。
type Handler struct {
	store    *store.Store
	sessions Sessions
	origin   OriginChecker
}

// NewHandler 注入存储、会话和同源能力。
func NewHandler(s *store.Store, sessions Sessions, origin OriginChecker) *Handler {
	return &Handler{s, sessions, origin}
}

type route struct{ path, allowed, required string }

var routes = []route{
	{"/api/usage/list-templates", "after limit", ""},
	{"/api/usage/create-template", "name description config", "name config"},
	{"/api/usage/update-template", "template_id expected_version name description config", "template_id expected_version name config"},
	{"/api/usage/delete-template", "template_id expected_version", "template_id expected_version"},
}

// RegisterRoutes 按唯一端点表注册POST入口。
func (h *Handler) RegisterRoutes(r *router.Router, middlewares ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range routes {
		r.POST(rt.path, lib.ChainMiddlewares(h.serve(rt), middlewares...))
	}
}

// OwnsRoute 仅精确匹配端点表中的POST路径。
func (h *Handler) OwnsRoute(method, path string) bool {
	if method != fasthttp.MethodPost {
		return false
	}
	for _, rt := range routes {
		if path == rt.path {
			return true
		}
	}
	return false
}

type rateDTO struct {
	TokenMax      int64 `json:"token_max,omitempty"`
	RequestMax    int64 `json:"request_max,omitempty"`
	WindowSeconds int   `json:"window_seconds"`
}
type configDTO struct {
	MaxCostUSD    float64             `json:"max_cost_usd"`
	ResetDuration usage.ResetDuration `json:"reset_duration"`
	AllowedModels []string            `json:"allowed_models"`
	RateLimit     *rateDTO            `json:"rate_limit,omitempty"`
}
type templateDTO struct {
	ID          string    `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Config      configDTO `json:"config"`
	Version     int64     `json:"version,omitempty"`
}
type input struct {
	templateDTO
	TemplateID      string `json:"template_id"`
	ExpectedVersion int64  `json:"expected_version"`
	After           string `json:"after"`
	Limit           int    `json:"limit"`
}
type saved struct {
	TemplateID string `json:"template_id"`
	Version    int64  `json:"version"`
}
type page struct {
	Templates []templateDTO `json:"templates"`
	NextAfter string        `json:"next_after"`
}

func (v configDTO) config() usage.Config {
	c := usage.Config{MaxCostUSD: v.MaxCostUSD, ResetDuration: v.ResetDuration, AllowedModels: v.AllowedModels}
	if v.RateLimit != nil {
		c.RateLimit = &usage.RateLimit{TokenMax: v.RateLimit.TokenMax, RequestMax: v.RateLimit.RequestMax, WindowSeconds: v.RateLimit.WindowSeconds}
	}
	return c
}
func present(t usage.Template) templateDTO {
	c := t.Config
	v := templateDTO{ID: t.ID, Name: t.Name, Description: t.Description, Version: t.Version,
		Config: configDTO{MaxCostUSD: c.MaxCostUSD, ResetDuration: c.ResetDuration, AllowedModels: c.AllowedModels}}
	if c.RateLimit != nil {
		v.Config.RateLimit = &rateDTO{TokenMax: c.RateLimit.TokenMax, RequestMax: c.RateLimit.RequestMax, WindowSeconds: c.RateLimit.WindowSeconds}
	}
	return v
}

// object只检查已知对象层次；重复字段由共用JSON验证器拒绝。
func object(raw []byte, allowed, required string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, identity.ErrInvalid
	}
	for key, value := range fields {
		if !slices.Contains(strings.Fields(allowed), key) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, identity.ErrInvalid
		}
	}
	for _, key := range strings.Fields(required) {
		if _, ok := fields[key]; !ok {
			return nil, identity.ErrInvalid
		}
	}
	return fields, nil
}

func decode(c *fasthttp.RequestCtx, rt route) (input, error) {
	v := input{Limit: usage.DefaultPageSize, templateDTO: templateDTO{Config: configDTO{ResetDuration: usage.Month}}}
	body := c.PostBody()
	media, _, err := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if err != nil || media != "application/json" || len(body) > authhttp.MaxBodyBytes || !utf8.Valid(body) || authhttp.ValidateJSONObject(body) != nil {
		return v, identity.ErrInvalid
	}
	fields, err := object(body, rt.allowed, rt.required)
	if err != nil || json.Unmarshal(body, &v) != nil {
		return v, identity.ErrInvalid
	}
	if raw, ok := fields["config"]; ok {
		fields, err = object(raw, "max_cost_usd reset_duration allowed_models rate_limit", "max_cost_usd allowed_models")
		if err != nil || v.Config.ResetDuration == "" {
			return v, identity.ErrInvalid
		}
		if raw, ok = fields["rate_limit"]; ok {
			rates, e := object(raw, "token_max request_max window_seconds", "window_seconds")
			if e != nil {
				return v, e
			}
			for key := range rates {
				if (key == "token_max" && v.Config.RateLimit.TokenMax <= 0) || (key == "request_max" && v.Config.RateLimit.RequestMax <= 0) {
					return v, identity.ErrInvalid
				}
			}
		}
	}
	if v.Limit < 1 || v.Limit > usage.MaxPageSize || (v.After != "" && !identity.ValidAccountID(v.After)) {
		return v, identity.ErrInvalid
	}
	if strings.Contains(rt.required, "template_id") && (!identity.ValidAccountID(v.TemplateID) || v.ExpectedVersion < 1) {
		return v, identity.ErrInvalid
	}
	v.TemplateID, v.After = strings.ToLower(v.TemplateID), strings.ToLower(v.After)
	return v, nil
}

func (h *Handler) serve(rt route) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		c.Response.Header.Set("Cache-Control", "no-store")
		if h.store == nil || h.sessions == nil || h.origin == nil {
			authhttp.Error(c, identity.ErrUnavailable)
			return
		}
		if !h.origin.SameOrigin(c) {
			authhttp.Error(c, identity.ErrForbidden)
			return
		}
		if len(c.Request.Header.Peek("Authorization")) > 0 {
			authhttp.Error(c, identity.ErrUnauthorized)
			return
		}
		ctx := authhttp.OperationContext(c, "usage"+rt.path)
		actor, err := h.sessions.Authenticate(ctx, string(c.Request.Header.Cookie(authhttp.CookieName)))
		if err != nil {
			authhttp.Error(c, err)
			return
		}
		v, err := decode(c, rt)
		if err != nil {
			authhttp.Error(c, err)
			return
		}
		status, body, err := h.execute(ctx, actor, rt.path, v)
		if err != nil {
			authhttp.Error(c, err)
			return
		}
		if status == fasthttp.StatusNoContent {
			c.SetStatusCode(status)
			return
		}
		authhttp.JSON(c, status, body)
	}
}

func (h *Handler) execute(ctx context.Context, actor identity.Principal, path string, v input) (int, any, error) {
	switch path {
	case routes[0].path:
		items, next, e := h.store.List(ctx, actor, v.After, v.Limit)
		out := page{Templates: []templateDTO{}, NextAfter: next}
		for _, t := range items {
			out.Templates = append(out.Templates, present(t))
		}
		return fasthttp.StatusOK, out, e
	case routes[3].path:
		return fasthttp.StatusNoContent, nil, h.store.Delete(ctx, actor, v.TemplateID, v.ExpectedVersion)
	default:
		t := usage.Template{ID: v.TemplateID, Name: v.Name, Description: v.Description, Config: v.Config.config(), Version: v.ExpectedVersion}
		var e error
		status := fasthttp.StatusOK
		if path == routes[1].path {
			t, e = h.store.Create(ctx, actor, t)
			status = fasthttp.StatusCreated
		} else {
			t, e = h.store.Update(ctx, actor, t)
		}
		return status, saved{TemplateID: t.ID, Version: t.Version}, e
	}
}
