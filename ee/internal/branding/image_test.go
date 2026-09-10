// 本文件验证图片解码、显式清除和损坏输入拒绝。
package branding

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func TestDecodeAsset(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	asset, err := DecodeAsset(base64.StdEncoding.EncodeToString(buf.Bytes()), "image/png")
	if err != nil || asset == nil || !bytes.Equal(asset.Data, buf.Bytes()) || asset.MIME != "image/png" {
		t.Fatalf("decoded image differs: %v", err)
	}
	for _, input := range []string{"data:;base64,", "\r\n", base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		if got, err := DecodeAsset(input, ""); err == nil || got != nil {
			t.Fatalf("invalid image accepted: %q", input)
		}
	}
	clear, err := DecodeAsset("", "")
	if err != nil || clear == nil || len(clear.Data) != 0 || clear.MIME != "" {
		t.Fatal("empty string must clear image")
	}
}
