package lib

import (
	"bytes"

	"github.com/valyala/fasthttp"
)

const eeShellMarker = `<meta name="x-bifrost-ee" content="1">`

// EEShellRewriter 在上游吐出 HTML 外壳前, 于 </head> 之前插入一个标记.
// 作用是证明 "上游 Bootstrap 之前赋值的字段会被上游读取" (ShellRewriter 在
// RegisterAPIRoutes 里被 NewUIHandler 捕获). 没有 </head> 时原样返回.
func EEShellRewriter(_ *fasthttp.RequestCtx, data []byte) []byte {
	head := []byte("</head>")
	if !bytes.Contains(data, head) {
		return data
	}
	return bytes.Replace(data, head, append([]byte(eeShellMarker), head...), 1)
}
