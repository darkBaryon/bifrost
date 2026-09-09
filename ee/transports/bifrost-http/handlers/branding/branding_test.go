// 本文件验证品牌接口的路由、鉴权、设置读写、输入校验与图片缓存。
package branding

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	eeconfig "github.com/darkBaryon/bifrost/ee/framework/configstore/branding"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	upstreamconfig "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/encrypt"
	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func imageBytes(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	var err error
	if format == "png" {
		err = png.Encode(&b, img)
	} else {
		err = jpeg.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func brandingTestRouter(t *testing.T) (*router.Router, *upstream.AuthMiddleware, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	if err := eeconfig.MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	am := &upstream.AuthMiddleware{}
	r := router.New()
	if err := NewBrandingHandler(eeconfig.NewBrandingStore(db)).RegisterRoutes(r, am.APIMiddleware()); err != nil {
		t.Fatal(err)
	}
	return r, am, db
}
func brandingRequest(r *router.Router, method, path, body string, headers map[string]string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Init(&fasthttp.Request{}, nil, nil)
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	ctx.Request.SetBodyString(body)
	for k, v := range headers {
		ctx.Request.Header.Set(k, v)
	}
	r.Handler(ctx)
	return ctx
}
func decodeResponse(t *testing.T, ctx *fasthttp.RequestCtx) brandingResponse {
	t.Helper()
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var response brandingResponse
	if err := json.Unmarshal(ctx.Response.Body(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestBrandingRealAdminMiddleware(t *testing.T) {
	r, am, _ := brandingTestRouter(t)
	hash, err := encrypt.Hash("test-password")
	if err != nil {
		t.Fatal(err)
	}
	am.UpdateAuthConfig(&upstreamconfig.AuthConfig{IsEnabled: true, AdminUserName: schemas.NewSecretVar("admin"), AdminPassword: schemas.NewSecretVar(hash)})
	for _, action := range []string{"update", "reset"} {
		for _, headers := range []map[string]string{nil, {"x-bf-vk": "not-an-admin"}, {"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:wrong"))}} {
			if got := brandingRequest(r, "POST", "/api/branding/"+action, `{"logo":""}`, headers).Response.StatusCode(); got != 401 {
				t.Fatalf("%s unauthorized status %d", action, got)
			}
		}
		decodeResponse(t, brandingRequest(r, "POST", "/api/branding/"+action, `{"logo":""}`, map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("admin:test-password"))}))
	}
	decodeResponse(t, brandingRequest(r, "POST", "/api/branding/get", "", nil))
	if NewBrandingHandler(nil).RegisterRoutes(router.New(), nil) == nil {
		t.Fatal("nil auth allowed")
	}
}

func TestBrandingActionRoutes(t *testing.T) {
	r, _, _ := brandingTestRouter(t)
	for _, action := range []string{"get", "update", "reset"} {
		path := "/api/branding/" + action
		if got := brandingRequest(r, "POST", path, `{"logo":""}`, nil).Response.StatusCode(); got != 200 {
			t.Fatalf("POST %s status %d", path, got)
		}
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if got := brandingRequest(r, method, path, `{"logo":""}`, nil).Response.StatusCode(); got < 400 {
				t.Fatalf("%s %s unexpectedly accepted: %d", method, path, got)
			}
		}
	}
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		if got := brandingRequest(r, method, "/api/branding", `{"logo":""}`, nil).Response.StatusCode(); got < 400 {
			t.Fatalf("old route %s unexpectedly accepted: %d", method, got)
		}
	}
}

func TestBrandingDefaultAndAssets(t *testing.T) {
	r, _, _ := brandingTestRouter(t)
	response := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/get", "", nil))
	if response.Enabled || response.HasLogo || response.HasIcon {
		t.Fatalf("default %+v", response)
	}
	data := imageBytes(t, "png", 3, 2)
	body := `{"logo":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(data) + `"}`
	response = decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", body, nil))
	if !response.HasLogo || response.HasIcon || !response.Enabled {
		t.Fatalf("response %+v", response)
	}
	url := response.LogoURL
	ctx := brandingRequest(r, "GET", url, "", nil)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("asset status %d", ctx.Response.StatusCode())
	}
	if !bytes.Equal(ctx.Response.Body(), data) {
		t.Fatal("asset body differs from uploaded bytes")
	}
	if ct := string(ctx.Response.Header.ContentType()); ct != "image/png" {
		t.Fatalf("asset content type %q", ct)
	}
	if v := string(ctx.Response.Header.Peek("X-Content-Type-Options")); v != "nosniff" {
		t.Fatalf("X-Content-Type-Options %q", v)
	}
	etag := string(ctx.Response.Header.Peek("ETag"))
	if brandingRequest(r, "GET", url, "", map[string]string{"If-None-Match": etag}).Response.StatusCode() != 304 {
		t.Fatal("missing 304")
	}
	response = decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", `{"icon":"`+base64.StdEncoding.EncodeToString(imageBytes(t, "jpeg", 2, 2))+`"}`, nil))
	if !response.HasIcon {
		t.Fatal("icon write not reflected")
	}
	if response.LogoURL != url {
		t.Fatalf("icon write changed logo url: %q", response.LogoURL)
	}
	response = decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", `{"icon":""}`, nil))
	if response.HasIcon {
		t.Fatal("icon not cleared")
	}
	if response.LogoURL != url {
		t.Fatalf("clearing icon changed logo url: %q", response.LogoURL)
	}
	for i := 0; i < 2; i++ {
		response = decodeResponse(t, brandingRequest(r, "POST", "/api/branding/reset", "", nil))
		if response.Enabled {
			t.Fatal("reset failed")
		}
	}
	if brandingRequest(r, "GET", url, "", map[string]string{"If-None-Match": etag}).Response.StatusCode() != 404 {
		t.Fatal("old etag survived reset")
	}
}

func TestBrandingInvalidPayloadAtomic(t *testing.T) {
	r, _, db := brandingTestRouter(t)
	good := base64.StdEncoding.EncodeToString(imageBytes(t, "png", 2, 2))
	before := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", `{"logo":"`+good+`"}`, nil))
	for _, body := range []string{`{}`, `null`, `[]`, `{"extra":1}`, `{"logo":null}`, `{"logo":2}`, `{"logo_mime":"image/png"}`, `{"logo":"bad"}`, `{"logo":"` + good + `","logo_mime":"image/jpeg"}`, `{"logo":"` + good + `","icon":"broken"}`, `{"icon":""} {}`, `{"logo":"` + base64.StdEncoding.EncodeToString([]byte("<svg/>")) + `"}`} {
		if ctx := brandingRequest(r, "POST", "/api/branding/update", body, nil); ctx.Response.StatusCode() != 400 {
			t.Fatalf("invalid accepted %s: %d", body[:min(len(body), 60)], ctx.Response.StatusCode())
		}
		after := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/get", "", nil))
		if before != after {
			t.Fatal("invalid payload changed response")
		}
	}
	if brandingRequest(r, "POST", "/api/branding/update", strings.Repeat("x", maxBrandingBodyBytes+1), nil).Response.StatusCode() != 413 {
		t.Fatal("body limit")
	}
	if brandingRequest(r, "POST", "/api/branding/update", `{"logo":"`+strings.Repeat("A", base64.StdEncoding.EncodedLen(maxBrandingImageBytes)+4)+`"}`, nil).Response.StatusCode() != 413 {
		t.Fatal("image limit")
	}
	oversized := imageBytes(t, "png", 4097, 1)
	if _, code, _ := decodeBrandingAsset(json.RawMessage(`"`+base64.StdEncoding.EncodeToString(oversized)+`"`), nil); code != 400 {
		t.Fatal("dimension limit")
	}
	truncated := imageBytes(t, "png", 2, 2)
	truncated = truncated[:len(truncated)-12]
	if _, code, _ := decodeBrandingAsset(json.RawMessage(`"`+base64.StdEncoding.EncodeToString(truncated)+`"`), nil); code != 400 {
		t.Fatal("truncated accepted")
	}
	sql, _ := db.DB()
	sql.Close()
	if brandingRequest(r, "POST", "/api/branding/get", "", nil).Response.StatusCode() != 500 {
		t.Fatal("DB failure disguised as defaults")
	}
}

// 写入失败时 update/reset 返回 500，旧图与版本保持不变，故障解除后重试成功。
func TestBrandingWriteFailureKeepsState(t *testing.T) {
	r, _, db := brandingTestRouter(t)
	good := base64.StdEncoding.EncodeToString(imageBytes(t, "png", 2, 2))
	before := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", `{"logo":"`+good+`"}`, nil))
	db.Callback().Create().Before("gorm:create").Register("test:write-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_branding" {
			tx.AddError(errors.New("injected write failure"))
		}
	})
	icon := `{"icon":"` + base64.StdEncoding.EncodeToString(imageBytes(t, "jpeg", 2, 2)) + `"}`
	for _, req := range []struct{ path, body string }{{"/api/branding/update", icon}, {"/api/branding/reset", ""}} {
		ctx := brandingRequest(r, "POST", req.path, req.body, nil)
		if ctx.Response.StatusCode() != 500 {
			t.Fatalf("%s during write failure: status %d", req.path, ctx.Response.StatusCode())
		}
		if strings.Contains(string(ctx.Response.Body()), "injected") {
			t.Fatalf("%s leaked internal error: %s", req.path, ctx.Response.Body())
		}
		if after := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/get", "", nil)); after != before {
			t.Fatalf("%s changed state during write failure: %+v", req.path, after)
		}
	}
	db.Callback().Create().Remove("test:write-failure")
	if state := decodeResponse(t, brandingRequest(r, "POST", "/api/branding/update", icon, nil)); !state.HasIcon || state.LogoURL != before.LogoURL {
		t.Fatalf("retry after recovery: %+v", state)
	}
}
