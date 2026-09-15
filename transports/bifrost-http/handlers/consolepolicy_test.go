package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/valyala/fasthttp"
)

// 未注入时允许原handler继续；错误类型或空回调必须拒绝，不能当作没有配置。
func TestProviderPolicyOptionalAndInvalid(t *testing.T) {
	var typedNil ConsoleProviderUpdatePolicy
	valid := ConsoleProviderUpdatePolicy(func(context.Context, *configstore.ProviderConfig, *schemas.NetworkConfig, *schemas.ProxyConfig) error {
		return nil
	})
	for _, tt := range []struct {
		name           string
		value          any
		present, valid bool
	}{
		{"absent", nil, false, true}, {"wrong-type", "invalid", true, false}, {"typed-nil", typedNil, true, false}, {"configured", valid, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &fasthttp.RequestCtx{}
			if tt.value != nil {
				c.SetUserValue(ConsoleProviderUpdatePolicyContextKey, tt.value)
			}
			_, present, ok := consolePolicy[ConsoleProviderUpdatePolicy](c, ConsoleProviderUpdatePolicyContextKey)
			if present != tt.present || ok != tt.valid {
				t.Fatalf("present=%v valid=%v", present, ok)
			}
			if !ok && c.Response.StatusCode() != 503 {
				t.Fatal("invalid policy did not fail closed")
			}
		})
	}
}

func TestProviderPolicyErrorsHideInternalText(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{errors.New("private-credential"), 503}, {&ConsolePolicyError{Status: 400, Message: "private-credential"}, 400},
	} {
		c := &fasthttp.RequestCtx{}
		sendConsolePolicyError(c, tt.err)
		if c.Response.StatusCode() != tt.status || strings.Contains(string(c.Response.Body()), "private-credential") {
			t.Fatal("unsafe policy error")
		}
	}
}
