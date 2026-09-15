// 本文件处理 /api/config 的认证投影与写入预检：GET 用只读投影替换旧认证字段，PUT 拒绝实际的认证或白名单变更。
package host

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"
)

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

// projection 输出固定恢复锚点的只读认证信息；独立认证兼容模式仍查询当前主管理员。
func (a *AuthAdapter) projection(ctx context.Context, p identity.Principal) (map[string]any, error) {
	var account identity.Account
	var err error
	if a.anchor != nil {
		account, err = a.anchor(ctx)
	} else {
		account, err = a.service.Me(ctx, p)
	}
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
