package rbachttp

import (
	"errors"
	"fmt"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// 自有接口与宿主策略必须对同一业务错误给出一致状态，未知错误不能泄漏原文。
func TestBusinessErrorResponse(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{rbac.ErrUnauthorized, fasthttp.StatusUnauthorized, "unauthorized"}, {rbac.ErrForbidden, fasthttp.StatusForbidden, "forbidden"},
		{rbac.ErrInvalid, fasthttp.StatusBadRequest, "invalid_input"}, {rbac.ErrNotFound, fasthttp.StatusNotFound, "not_found"},
		{rbac.ErrConflict, fasthttp.StatusConflict, "conflict"}, {rbac.ErrUnavailable, fasthttp.StatusServiceUnavailable, "unavailable"},
		{errors.New("SECRET"), fasthttp.StatusServiceUnavailable, "unavailable"}, {rbac.Error("future"), fasthttp.StatusServiceUnavailable, "unavailable"},
	}
	for _, test := range cases {
		wrapped := fmt.Errorf("wrapped: %w", test.err)
		c := &fasthttp.RequestCtx{}
		Error(c, wrapped)
		if c.Response.StatusCode() != test.status || Status(wrapped) != test.status || gjson.GetBytes(c.Response.Body(), "error.code").String() != test.code {
			t.Fatalf("%v: status=%d body=%s", test.err, c.Response.StatusCode(), c.Response.Body())
		}
	}
	c := &fasthttp.RequestCtx{}
	Error(c, &rbac.ConflictError{AccountCount: 7})
	if c.Response.StatusCode() != fasthttp.StatusConflict || gjson.GetBytes(c.Response.Body(), "error.details.account_count").Int() != 7 {
		t.Fatal("lost conflict details")
	}
	for _, status := range []int{fasthttp.StatusMethodNotAllowed, fasthttp.StatusInternalServerError} {
		Error(c, FromStatus(status))
		if c.Response.StatusCode() != status {
			t.Fatalf("host status changed: %d", status)
		}
	}
}
