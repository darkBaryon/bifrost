// 本文件记录每个已有接口的权限要求，并在启动时核对Bifrost实际注册的路由。
package host

import (
	_ "embed"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// kind 说明谁负责检查这个接口；managed由本包检查，其他类别交给各自的处理器。
type kind string

const (
	kindIdentityService kind = "identity-service"
	kindInference       kind = "inference"
	kindManaged         kind = "managed"
	kindOauthProtocol   kind = "oauth-protocol"
	kindPublic          kind = "public"
	kindRbacService     kind = "rbac-service"
	kindStatic          kind = "static"
	kindVkSelf          kind = "vk-self"
)

// guard 标记请求还需检查什么，例如导出虚拟密钥要额外检查查看密钥权限。
type guard string

const (
	guardBrandingService    guard = "branding-service"
	guardLogsQuery          guard = "logs-query"
	guardNone               guard = "none"
	guardNotificationPolicy guard = "notification-policy"
	guardPluginMutation     guard = "plugin-mutation"
	guardProviderInput      guard = "provider-input"
	guardSettingsInput      guard = "settings-input"
	guardVkExport           guard = "vk-export"
	guardWebhookInput       guard = "webhook-input"
	guardWebsocket          guard = "websocket"
)

// projection 指定返回结果要隐藏哪些内容；protocol表示由原接口处理，不改响应。
type projection string

const (
	projectionBrandingSafe       projection = "branding-safe"
	projectionDebugExplicit      projection = "debug-explicit"
	projectionGovernanceNestedVk projection = "governance-nested-vk"
	projectionKeyMetadata        projection = "key-metadata"
	projectionLogsContent        projection = "logs-content"
	projectionMcpSafe            projection = "mcp-safe"
	projectionMessageFilter      projection = "message-filter"
	projectionNotificationPolicy projection = "notification-policy"
	projectionOrdinary           projection = "ordinary"
	projectionPluginSafe         projection = "plugin-safe"
	projectionPromptContent      projection = "prompt-content"
	projectionProtocol           projection = "protocol"
	projectionProtocolScoped     projection = "protocol-scoped"
	projectionProviderSafe       projection = "provider-safe"
	projectionRoutingSafe        projection = "routing-safe"
	projectionSettingsSafe       projection = "settings-safe"
	projectionSkillContent       projection = "skill-content"
	projectionVkValues           projection = "vk-values"
	projectionWebhookSafe        projection = "webhook-safe"
)

// routeEntry 将一个完整接口地址与权限、额外检查和响应处理放在一起。
type routeEntry struct {
	Method      string
	Pattern     string
	Kind        kind
	Permissions []rbac.Permission
	Guard       guard
	Projection  projection
}

// routes.txt只存运行所需的规则；同一组规则可以包含多个明确列出的接口。
//
//go:embed routes.txt
var routeManifestText []byte

var routeMethods = []string{fasthttp.MethodGet, fasthttp.MethodPost, fasthttp.MethodPut, fasthttp.MethodPatch, fasthttp.MethodDelete, fasthttp.MethodHead, fasthttp.MethodOptions}
var routeDefinitions, routeManifestError = decodeRouteManifest(routeManifestText)

func manifest() []routeEntry { return routeDefinitions }

// decodeRouteManifest 读取分组清单。未知类别、权限或处理方式以及重复接口都会报错，阻止启动。
func decodeRouteManifest(raw []byte) ([]routeEntry, error) {
	var entries []routeEntry
	var group routeEntry
	seen := map[string]bool{}
	for number, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		invalid := func() error { return fmt.Errorf("invalid route rule at line %d", number+1) }
		if fields[0] == "@" {
			if len(fields) != 5 {
				return nil, invalid()
			}
			group = routeEntry{Kind: kind(fields[1]), Guard: guard(fields[3]), Projection: projection(fields[4])}
			if fields[2] != "-" {
				for _, code := range strings.Split(fields[2], ",") {
					permission := rbac.Permission(code)
					if !rbac.KnownPermission(permission) {
						return nil, invalid()
					}
					group.Permissions = append(group.Permissions, permission)
				}
			}
			if !slices.Contains([]kind{kindIdentityService, kindInference, kindManaged, kindOauthProtocol, kindPublic, kindRbacService, kindStatic, kindVkSelf}, group.Kind) ||
				!slices.Contains([]guard{guardBrandingService, guardLogsQuery, guardNone, guardNotificationPolicy, guardPluginMutation, guardProviderInput, guardSettingsInput, guardVkExport, guardWebhookInput, guardWebsocket}, group.Guard) ||
				!slices.Contains([]projection{projectionBrandingSafe, projectionDebugExplicit, projectionGovernanceNestedVk, projectionKeyMetadata, projectionLogsContent, projectionMcpSafe, projectionMessageFilter, projectionNotificationPolicy, projectionOrdinary, projectionPluginSafe, projectionPromptContent, projectionProtocol, projectionProtocolScoped, projectionProviderSafe, projectionRoutingSafe, projectionSettingsSafe, projectionSkillContent, projectionVkValues, projectionWebhookSafe}, group.Projection) {
				return nil, invalid()
			}
			if group.Kind == kindManaged && len(group.Permissions) == 0 {
				return nil, invalid()
			}
			if group.Kind != kindManaged && (len(group.Permissions) != 0 || group.Guard != guardNone || group.Projection != projectionProtocol) {
				return nil, invalid()
			}
			continue
		}
		if len(fields) != 2 || group.Kind == "" || !strings.HasPrefix(fields[1], "/") {
			return nil, invalid()
		}
		for _, method := range strings.Split(fields[0], ",") {
			key := method + " " + fields[1]
			if !slices.Contains(routeMethods, method) || seen[key] {
				return nil, invalid()
			}
			seen[key] = true
			entry := group
			entry.Method, entry.Pattern = method, fields[1]
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("empty route rules")
	}
	return entries, nil
}

// matchedRouteKey 只用于从独立匹配器取出接口规则，不写入业务请求。
type matchedRouteKey struct{}

// VerifyRoutes 在所有接口注册完成后检查是否都已登记权限规则；漏登记就拒绝启动。
func (a *Adapter) VerifyRoutes(actual *router.Router) error {
	if routeManifestError != nil {
		return routeManifestError
	}
	entries := map[string]routeEntry{}
	for _, r := range manifest() {
		key := r.Method + " " + r.Pattern
		entries[key] = r
	}
	matcher := router.New()
	matcher.RedirectTrailingSlash = false
	matcher.RedirectFixedPath = false
	for method, paths := range actual.List() {
		for _, pattern := range paths {
			key := method + " " + pattern
			entry, ok := entries[key]
			if !ok {
				return fmt.Errorf("unclassified route: %s", key)
			}
			matcher.Handle(method, pattern, func(c *fasthttp.RequestCtx) { c.SetUserValue(matchedRouteKey{}, entry) })
		}
	}
	names := lib.GetBuiltinPluginNames()
	if len(names) != len(builtinPermissions) {
		return fmt.Errorf("builtin plugin catalogue changed")
	}
	for _, name := range names {
		if _, ok := builtinPermissions[name]; !ok {
			return fmt.Errorf("unclassified builtin plugin: %s", name)
		}
	}
	a.matcher = matcher
	return nil
}

// match 使用启动时核对过的路由匹配完整请求方法和路径，也支持路径中的角色编号等参数。
func (a *Adapter) match(method, path string) (routeEntry, bool) {
	if a.matcher == nil {
		return routeEntry{}, false
	}
	scratch := &fasthttp.RequestCtx{}
	handler, _ := a.matcher.Lookup(method, path, scratch)
	if handler == nil {
		return routeEntry{}, false
	}
	handler(scratch)
	entry, ok := scratch.UserValue(matchedRouteKey{}).(routeEntry)
	return entry, ok
}

func apiPath(p string) bool { return p == "/api" || strings.HasPrefix(p, "/api/") }

// canonical 拒绝重复斜杠、点路径和编码斜杠等写法，保证权限检查与实际路由看到同一个地址。
func canonical(c *fasthttp.RequestCtx) bool {
	p := string(c.Path())
	raw := strings.ToLower(string(c.URI().PathOriginal()))
	if strings.Contains(raw, "%2f") || strings.Contains(raw, "%5c") || strings.Contains(raw, "%25") || strings.Contains(raw, "%2e") || strings.Contains(raw, "\\") {
		return false
	}
	// fasthttp会先规范化点段，因此还需核对原始路径。
	original := string(c.URI().PathOriginal())
	if strings.Contains(original, "//") || (path.Clean(original) != strings.TrimSuffix(original, "/") && original != "/") {
		return false
	}
	return p == "/" || !strings.HasSuffix(p, "/")
}

// RootGuard 在路由分发前拒绝未知API和异常路径，防止请求误入前端页面或绕过接口匹配。
func (a *Adapter) RootGuard(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		p := string(c.Path())
		method := string(c.Method())
		if !canonical(c) {
			rbachttp.Error(c, rbac.ErrInvalid)
			return
		}
		entry, ok := a.match(method, p)
		if apiPath(p) && (!ok || entry.Kind == kindStatic) {
			var allowed []string
			for _, m := range routeMethods {
				if e, found := a.match(m, p); found && e.Kind != kindStatic {
					allowed = append(allowed, m)
				}
			}
			if len(allowed) > 0 {
				c.Response.Header.Set("Allow", strings.Join(allowed, ", "))
				c.Response.Header.Set("Cache-Control", "no-store")
				rbachttp.Error(c, rbachttp.FromStatus(fasthttp.StatusMethodNotAllowed))
				return
			}
			rbachttp.Error(c, rbac.ErrNotFound)
			return
		}
		next(c)
	}
}
