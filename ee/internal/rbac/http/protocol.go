// 本文件定义严格输入协议，不重复实现Cookie认证。
package rbachttp

import (
	"bytes"
	"encoding/json"
	"mime"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

// 请求上限与账号UUID格式分别沿用identity/http/protocol.go及identity/model.go；角色ID另按整数解析。
const maxBodyBytes = 16 << 10

var accountPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func decode(c *fasthttp.RequestCtx, allowed, required []string) (map[string]json.RawMessage, error) {
	body := c.PostBody()
	media, _, e := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if e != nil || media != "application/json" || len(body) > maxBodyBytes || !utf8.Valid(body) || authhttp.ValidateJSONObject(body) != nil {
		return nil, rbac.ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return nil, rbac.ErrInvalid
	}
	for k, v := range fields {
		known := false
		for _, name := range allowed {
			known = known || name == k
		}
		if !known || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, rbac.ErrInvalid
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return nil, rbac.ErrInvalid
		}
	}
	return fields, nil
}

func textField(fields map[string]json.RawMessage, key string) (string, error) {
	var out string
	if v, ok := fields[key]; ok && json.Unmarshal(v, &out) != nil {
		return "", rbac.ErrInvalid
	}
	return out, nil
}

func roleID(raw json.RawMessage) (rbac.RoleID, error) {
	if len(raw) == 0 {
		return 0, rbac.ErrInvalid
	}
	for _, b := range raw {
		if b < '0' || b > '9' {
			return 0, rbac.ErrInvalid
		}
	}
	n, e := strconv.ParseUint(string(raw), 10, 64)
	if e != nil || n == 0 || n > uint64(rbac.MaxRoleID) {
		return 0, rbac.ErrInvalid
	}
	return rbac.RoleID(n), nil
}

func accountID(fields map[string]json.RawMessage) (string, error) {
	id, e := textField(fields, "account_id")
	if e != nil || !accountPattern.MatchString(id) {
		return "", rbac.ErrInvalid
	}
	return strings.ToLower(id), nil
}

func roleInput(fields map[string]json.RawMessage) (rbac.RoleInput, error) {
	var in rbac.RoleInput
	var e error
	in.Name, e = textField(fields, "name")
	if e != nil {
		return in, e
	}
	in.Description, e = textField(fields, "description")
	if e != nil {
		return in, e
	}
	// [] 是清空权限；null 和非字符串元素均拒绝。
	var raw []json.RawMessage
	if json.Unmarshal(fields["permission_codes"], &raw) != nil || raw == nil {
		return in, rbac.ErrInvalid
	}
	in.PermissionCodes = []rbac.Permission{}
	for _, v := range raw {
		var code string
		if bytes.Equal(v, []byte("null")) || json.Unmarshal(v, &code) != nil {
			return in, rbac.ErrInvalid
		}
		in.PermissionCodes = append(in.PermissionCodes, rbac.Permission(code))
	}
	return in, nil
}
