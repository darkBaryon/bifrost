// 本文件把认证和会话业务错误转换为 HTTP 错误，底层错误只返回 unavailable。
package authhttp

import (
	"errors"
	"strconv"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/valyala/fasthttp"
)

// Error 输出认证、会话或 HTTP 协议错误；未知类型与未知错误码隐藏为 unavailable。
func Error(c *fasthttp.RequestCtx, err error) {
	var authenticationError auth.Error
	var sessionError session.Error
	switch {
	case errors.As(err, &authenticationError):
		err = httpx.ErrorCode(authenticationError)
	case errors.As(err, &sessionError):
		err = httpx.ErrorCode(sessionError)
	}
	if errors.Is(err, httpx.ErrLimited) {
		c.Response.Header.Set("Retry-After", strconv.Itoa(int(auth.LoginRateWindow/time.Second)))
	}
	httpx.WriteError(c, err)
}
