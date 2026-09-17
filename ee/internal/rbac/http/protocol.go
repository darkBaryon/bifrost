// 本文件处理接口共用的JSON校验、字段解析和错误响应；内部错误原文不会返回给调用者。
package rbachttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

// —— 请求校验与字段解析 ——

// maxBodyBytes 将请求正文限制为16 KiB，与身份接口使用相同的上限。
const maxBodyBytes = authhttp.MaxBodyBytes

// decode 只接受一个JSON对象；allowed列出允许的字段，required列出必须传的字段。
// 重复字段、未知字段、null、错误的内容类型、无效UTF-8或超大正文都会被拒绝。
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

// textField 读取字符串字段；没有这个字段时返回空字符串，传了其他类型则报错。
func textField(fields map[string]json.RawMessage, key string) (string, error) {
	var out string
	if v, ok := fields[key]; ok && json.Unmarshal(v, &out) != nil {
		return "", rbac.ErrInvalid
	}
	return out, nil
}

// roleID 读取角色编号，只接受范围内的正整数；"1"、1.0和1e0这样的写法也拒绝。
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

// accountID 检查账号编号的UUID格式，统一转为小写后交给业务服务。
func accountID(fields map[string]json.RawMessage) (string, error) {
	id, e := textField(fields, "account_id")
	if e != nil || !identity.ValidAccountID(id) {
		return "", rbac.ErrInvalid
	}
	return strings.ToLower(id), nil
}

// —— 错误响应 ——

// 常见业务错误与HTTP状态的对应关系，例如参数错误对应400、没有权限对应403。
var errorStatuses = []struct {
	error  rbac.Error
	status int
}{
	{rbac.ErrInvalid, fasthttp.StatusBadRequest},
	{rbac.ErrUnauthorized, fasthttp.StatusUnauthorized},
	{rbac.ErrForbidden, fasthttp.StatusForbidden},
	{rbac.ErrNotFound, fasthttp.StatusNotFound},
	{rbac.ErrConflict, fasthttp.StatusConflict},
	{rbac.ErrUnavailable, fasthttp.StatusServiceUnavailable},
}

const methodNotAllowed = "method_not_allowed"

// statusError 保存已经确定的HTTP状态及可公开返回的错误码。
type statusError struct {
	status int
	code   string
}

func (e statusError) Error() string { return e.code }

// Status 返回业务错误对应的HTTP状态；不认识的错误统一返回503，表示服务暂时不可用。
func Status(err error) int {
	_, status := classifiedError(err)
	return status
}

func classifiedError(err error) (rbac.Error, int) {
	for _, pair := range errorStatuses {
		if errors.Is(err, pair.error) {
			return pair.error, pair.status
		}
	}
	return rbac.ErrUnavailable, fasthttp.StatusServiceUnavailable
}

// Error 写回HTTP状态和JSON错误；角色仍被账号使用时，还会返回关联账号数。
// FromStatus生成的错误沿用指定状态；其他错误按业务错误分类，不返回内部错误原文。
func Error(c *fasthttp.RequestCtx, err error) {
	var hostStatus statusError
	if errors.As(err, &hostStatus) {
		authhttp.JSON(c, hostStatus.status, errorResponse{Error: errorDetail{Code: hostStatus.code, Message: hostStatus.code}})
		return
	}
	code, status := classifiedError(err)
	detail := errorDetail{Code: string(code), Message: string(code)}
	var conflict *rbac.ConflictError
	if errors.As(err, &conflict) {
		detail.Details = &conflictDetails{AccountCount: conflict.AccountCount}
	}
	authhttp.JSON(c, status, errorResponse{Error: detail})
}

// FromStatus 把HTTP状态包装成可交给Error输出的错误，例如错误请求方法对应405。
// 状态码原样保留；不认识的状态使用unavailable作为错误内容。
func FromStatus(status int) error {
	if status == fasthttp.StatusMethodNotAllowed {
		return statusError{status, methodNotAllowed}
	}
	for _, pair := range errorStatuses {
		if status == pair.status {
			return statusError{status, string(pair.error)}
		}
	}
	return statusError{status, string(rbac.ErrUnavailable)}
}

// errorResponse 让所有错误都放在JSON的error字段中。
type errorResponse struct {
	Error errorDetail `json:"error"`
}

// errorDetail 返回错误码和说明；只有需要额外说明时才包含details。
type errorDetail struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details *conflictDetails `json:"details,omitempty"`
}

// conflictDetails 告诉前端还有多少账号使用这个角色，便于说明为什么不能删除。
type conflictDetails struct {
	AccountCount int `json:"account_count"`
}
