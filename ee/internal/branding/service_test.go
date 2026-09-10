// 本文件验证品牌业务在脱离 HTTP 和数据库时仍保持输入、原子操作与资源版本边界。
package branding

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

type recordingRepository struct {
	settings Settings
	err      error
	reads    int
	writes   []Patch
}

func (r *recordingRepository) Read(context.Context) (Settings, error) {
	r.reads++
	return r.settings, r.err
}

func (r *recordingRepository) Update(_ context.Context, patch Patch) (Settings, error) {
	r.writes = append(r.writes, patch)
	return r.settings, r.err
}

func TestDecodeAssetPreservesValidatedValue(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	asset, err := DecodeAsset(base64.StdEncoding.EncodeToString(encoded.Bytes()), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	data := asset.Data()
	data[0] = 0
	repo := &recordingRepository{}
	if _, err := NewService(repo).Update(context.Background(), Patch{Logo: asset}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repo.writes[0].Logo.Data(), encoded.Bytes()) || asset.MIME() != "image/png" {
		t.Fatal("caller mutated validated image before storage")
	}
	for _, input := range []string{"data:;base64,", "\r\n", base64.StdEncoding.EncodeToString([]byte("not an image"))} {
		if got, err := DecodeAsset(input, ""); err == nil || got != nil {
			t.Fatalf("invalid input produced savable asset: %q", input)
		}
	}
	clear, err := DecodeAsset("", "")
	if err != nil || clear == nil || len(clear.Data()) != 0 || clear.MIME() != "" {
		t.Fatal("empty string must produce explicit clear")
	}
}

func TestServiceRejectsEmptyUpdateAndResetsAtomically(t *testing.T) {
	repo := &recordingRepository{}
	service := NewService(repo)
	var invalid *ValidationError
	if _, err := service.Update(context.Background(), Patch{}); !errors.As(err, &invalid) || len(repo.writes) != 0 {
		t.Fatal("empty update reached storage")
	}
	if _, err := service.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.writes) != 1 || repo.writes[0].Logo == nil || repo.writes[0].Icon == nil {
		t.Fatal("reset must clear both slots through one atomic update")
	}
	failure := errors.New("storage failed")
	repo.err = failure
	if _, err := service.Read(context.Background()); !errors.Is(err, failure) {
		t.Fatal("read hid storage failure")
	}
	if _, err := service.Reset(context.Background()); !errors.Is(err, failure) {
		t.Fatal("reset hid storage failure")
	}
}

func TestServiceSelectsCurrentAsset(t *testing.T) {
	hash := strings.Repeat("a", 64)
	repo := &recordingRepository{settings: Settings{Logo: []byte("stored-image"), LogoMIME: "image/png", LogoHash: hash}}
	service := NewService(repo)
	ctx := context.Background()
	asset, err := service.Asset(ctx, "logo", hash)
	if err != nil || string(asset.Data()) != "stored-image" || asset.MIME() != "image/png" {
		t.Fatalf("current image: %v", err)
	}
	for _, request := range [][2]string{{"icon", hash}, {"logo", strings.Repeat("b", 64)}} {
		if _, err := service.Asset(ctx, request[0], request[1]); !errors.Is(err, ErrAssetNotFound) {
			t.Fatal("missing or stale image was exposed")
		}
	}
	failure := errors.New("storage unavailable")
	repo.err = failure
	reads := repo.reads
	for _, request := range [][2]string{{"other", hash}, {"logo", "short"}} {
		if _, err := service.Asset(ctx, request[0], request[1]); !errors.Is(err, ErrAssetNotFound) || repo.reads != reads {
			t.Fatal("invalid path reached storage")
		}
	}
	if _, err := service.Asset(ctx, "logo", hash); !errors.Is(err, failure) {
		t.Fatal("valid path disguised storage failure as missing image")
	}
}
