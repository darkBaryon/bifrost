// 本文件将账号业务错误转换为安全的 HTTP 错误，并保存端点请求数据。
package identityhttp

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/auth/http"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/valyala/fasthttp"
)

func writeError(c *fasthttp.RequestCtx, err error) {
	var accountError identity.Error
	if errors.As(err, &accountError) {
		err = auth.Error(accountError)
	}
	authhttp.Error(c, err)
}

type request struct {
	ctx       context.Context
	c         *fasthttp.RequestCtx
	principal session.Principal
}

func (r request) decode(v any) error { return httpx.DecodeJSONObject(r.c, v) }
func (r request) cookie() string     { return string(r.c.Request.Header.Cookie(authhttp.CookieName)) }
