// 本文件在真实 BF 插件管线中验证短路和响应拦截；模型替身不访问任何外部服务。
package plugin_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

type noProviderAccount struct{}

func (noProviderAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) { return nil, nil }
func (noProviderAccount) GetKeysForProvider(context.Context, schemas.ModelProvider) ([]schemas.Key, error) {
	return nil, fmt.Errorf("test must not call a provider")
}
func (noProviderAccount) GetConfigForProvider(schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	// 宿主在 Hook 前读取配置；若测试替身未短路，禁止落到任何外部模型地址。
	return &schemas.ProviderConfig{NetworkConfig: schemas.NetworkConfig{BaseURL: "http://127.0.0.1:1"}}, nil
}

type modelStub struct {
	calls int
	text  string
}

func (*modelStub) GetName() string                                                       { return "test-model" }
func (*modelStub) Cleanup() error                                                        { return nil }
func (*modelStub) PreRequestHook(*schemas.BifrostContext, *schemas.BifrostRequest) error { return nil }
func (m *modelStub) PreLLMHook(_ *schemas.BifrostContext, r *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	m.calls++
	return r, &schemas.LLMPluginShortCircuit{Response: response(m.text)}, nil
}
func (*modelStub) PostLLMHook(_ *schemas.BifrostContext, r *schemas.BifrostResponse, e *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	return r, e, nil
}

func TestBifrostPipeline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stage    guardrails.Stage
		input    string
		output   string
		wantCode string
		calls    int
	}{
		{"input block", guardrails.Input, "unsafe", "safe", "content_safety_blocked", 0},
		{"output block", guardrails.Output, "safe", "unsafe", "content_safety_blocked", 1},
		{"pass", guardrails.Output, "safe", "safe", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newPlugin(t, []guardrails.Rule{testRule(tc.stage)}, matchingDetector)
			model := &modelStub{text: tc.output}
			gateway, err := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: noProviderAccount{}, LLMPlugins: []schemas.LLMPlugin{p, model}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError)})
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Shutdown()
			req := request(tc.input).ChatRequest
			got, bErr := gateway.ChatCompletionRequest(testContext(), req)
			if tc.wantCode != "" {
				requireCode(t, bErr, tc.wantCode)
				if got != nil {
					t.Fatal("blocked response leaked")
				}
			} else if bErr != nil || got == nil || *got.Choices[0].Message.Content.ContentStr != tc.output {
				t.Fatalf("got=%+v err=%+v", got, bErr)
			}
			if model.calls != tc.calls {
				t.Fatalf("model calls=%d want=%d", model.calls, tc.calls)
			}
		})
	}
}

func TestBifrostRejectsOutputStreamBeforeModel(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	model := &modelStub{text: "unsafe"}
	gateway, err := bifrost.Init(context.Background(), schemas.BifrostConfig{Account: noProviderAccount{}, LLMPlugins: []schemas.LLMPlugin{p, model}, Logger: bifrost.NewDefaultLogger(schemas.LogLevelError)})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.Shutdown()
	stream, bErr := gateway.ChatCompletionStreamRequest(testContext(), request("safe").ChatRequest)
	requireCode(t, bErr, "content_safety_output_stream_unsupported")
	if stream != nil || model.calls != 0 {
		t.Fatalf("stream=%v model calls=%d", stream, model.calls)
	}
}
