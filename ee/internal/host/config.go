// 本文件处理/api/config中的旧认证字段：读取时返回管理员名字和隐藏后的密码，保存时禁止修改认证设置或免登录白名单。
package host

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/valyala/fasthttp"
)

// serveConfig 在读取配置后替换旧认证字段；保存配置前先检查这些字段有没有被修改，再移除auth_config交给Bifrost处理。
func (a *AuthAdapter) serveConfig(ctx context.Context, c *fasthttp.RequestCtx, p session.Principal, next fasthttp.RequestHandler) {
	projection, err := a.projection(ctx, p)
	if err != nil {
		writeConsoleError(c, err)
		return
	}
	if c.IsPut() {
		if err = a.checkConfig(c, projection); err != nil {
			writeConsoleError(c, err)
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
			writeConsoleError(c, identity.ErrUnavailable)
			return
		}
		c.Response.SetBody(body)
	}
}

// projection 为旧auth_config字段生成只读内容：认证始终启用，返回初始化管理员的名字，密码显示为<redacted>。
// 未传入管理员查询函数时，使用当前操作者的信息。
func (a *AuthAdapter) projection(ctx context.Context, p session.Principal) (map[string]any, error) {
	var username string
	if a.anchor != nil {
		account, err := a.anchor(ctx)
		if err != nil {
			return nil, err
		}
		username = account.Username
	} else {
		account, err := a.authentication.Me(ctx, p)
		if err != nil {
			return nil, err
		}
		username = account.Username
	}
	return map[string]any{"is_enabled": true, "admin_username": schemas.NewSecretVar(username), "admin_password": schemas.NewSecretVar("<redacted>")}, nil
}

// checkConfig 检查保存的配置：旧认证内容必须与查询结果一致，免登录白名单必须为空，否则整个请求返回409。
// 检查通过后移除auth_config，避免Bifrost把它写回旧认证配置。
// Bifrost解析字段时不区分大小写，所以这里拒绝Auth_Config等变体，防止检查和实际写入认成不同字段。
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
	if err := httpx.ValidateJSONObject(body); err != nil {
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
