// 本文件验证纯文本提取覆盖全部消息和候选，未知载体与超限不能静默通过。
package plugin_test

import (
	"context"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/maximhq/bifrost/core/schemas"
)

func TestAllHistoryAndTextBlocks(t *testing.T) {
	var seen string
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, func(_ context.Context, text string) ([]guardrails.Finding, error) { seen = text; return nil, nil })
	req := request("system text")
	req.ChatRequest.Input[0].Role = schemas.ChatMessageRoleSystem
	m := message("")
	m.Content = &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("un")}, {Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("safe")}}}
	req.ChatRequest.Input = append(req.ChatRequest.Input, m, message("last message"))
	_, short, err := p.PreLLMHook(testContext(), req)
	if err != nil || short != nil || seen != "system text\nunsafe\nlast message" {
		t.Fatalf("seen=%q short=%+v err=%v", seen, short, err)
	}
}

func TestUnsupportedInputCarriers(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, matchingDetector)
	for _, tc := range []struct {
		name   string
		mutate func(*schemas.BifrostRequest, *schemas.BifrostContext)
	}{
		{"raw body", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.RawRequestBody = []byte("unsafe")
		}},
		{"raw flag", func(_ *schemas.BifrostRequest, c *schemas.BifrostContext) {
			c.SetValue(schemas.BifrostContextKeyUseRawRequestBody, true)
		}},
		{"large payload", func(_ *schemas.BifrostRequest, c *schemas.BifrostContext) {
			c.SetValue(schemas.BifrostContextKeyLargePayloadMode, true)
		}},
		{"large response threshold", func(_ *schemas.BifrostRequest, c *schemas.BifrostContext) {
			c.SetValue(schemas.BifrostContextKeyLargeResponseThreshold, int64(1024))
		}},
		{"other protocol", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) { r.RequestType = schemas.ResponsesRequest }},
		{"extra params", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Params = &schemas.ChatParameters{ExtraParams: map[string]any{"messages": "unsafe"}}
		}},
		{"tool definitions", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Params = &schemas.ChatParameters{Tools: []schemas.ChatTool{{}}}
		}},
		{"message name", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].Name = schemas.Ptr("unsafe")
		}},
		{"reasoning", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].ChatAssistantMessage = &schemas.ChatAssistantMessage{Reasoning: schemas.Ptr("unsafe")}
		}},
		{"tool result", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].Role = schemas.ChatMessageRoleTool
		}},
		{"no messages", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) { r.ChatRequest.Input = nil }},
		{"missing content", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) { r.ChatRequest.Input[0].Content = nil }},
		{"mixed block", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].Content = &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{Type: schemas.ChatContentBlockTypeText, Text: schemas.Ptr("safe"), Refusal: schemas.Ptr("unsafe")}}}
		}},
		{"image block", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].Content = &schemas.ChatMessageContent{ContentBlocks: []schemas.ChatContentBlock{{Type: schemas.ChatContentBlockTypeImage}}}
		}},
		{"dual content", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0].Content.ContentBlocks = []schemas.ChatContentBlock{}
		}},
		{"invalid utf8", func(r *schemas.BifrostRequest, _ *schemas.BifrostContext) {
			r.ChatRequest.Input[0] = message(string([]byte{0xff}))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := request("safe"), testContext()
			tc.mutate(r, c)
			_, short, err := p.PreLLMHook(c, r)
			if err != nil || short == nil {
				t.Fatalf("short=%+v err=%v", short, err)
			}
			requireCode(t, short.Error, "content_safety_unsupported_content")
		})
	}
}

func TestUnsupportedOutputCarriers(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)

	for _, tc := range []struct {
		name   string
		mutate func(*schemas.BifrostResponse)
	}{
		{"refusal", func(r *schemas.BifrostResponse) {
			r.ChatResponse.Choices[0].Message.ChatAssistantMessage = &schemas.ChatAssistantMessage{Refusal: schemas.Ptr("unsafe")}
		}},
		{"extra params", func(r *schemas.BifrostResponse) {
			r.ChatResponse.ExtraParams = map[string]any{"extra_content": "unsafe"}
		}},
		{"citations", func(r *schemas.BifrostResponse) { r.ChatResponse.Citations = []string{"unsafe"} }},
		{"raw response", func(r *schemas.BifrostResponse) { r.ChatResponse.ExtraFields.RawResponse = "unsafe raw response" }},
		{"raw request", func(r *schemas.BifrostResponse) { r.ChatResponse.ExtraFields.RawRequest = "unsafe raw request" }},
		{"stream choice", func(r *schemas.BifrostResponse) {
			r.ChatResponse.Choices[0].ChatStreamResponseChoice = &schemas.ChatStreamResponseChoice{}
		}},
		{"no choices", func(r *schemas.BifrostResponse) { r.ChatResponse.Choices = nil }},
		{"no message", func(r *schemas.BifrostResponse) { r.ChatResponse.Choices[0].Message = nil }},
		{"message name", func(r *schemas.BifrostResponse) { r.ChatResponse.Choices[0].Message.Name = schemas.Ptr("unsafe") }},
		{"stop text", func(r *schemas.BifrostResponse) { r.ChatResponse.Choices[0].StopString = schemas.Ptr("unsafe") }},
		{"content logprobs", func(r *schemas.BifrostResponse) {
			r.ChatResponse.Choices[0].LogProbs = &schemas.BifrostLogProbs{Content: []schemas.ContentLogProb{{Token: "unsafe"}}}
		}},
		{"candidate logprobs", func(r *schemas.BifrostResponse) {
			r.ChatResponse.Choices[0].LogProbs = &schemas.BifrostLogProbs{Content: []schemas.ContentLogProb{{Token: "safe", TopLogProbs: []schemas.LogProb{{Token: "unsafe"}}}}}
		}},
		{"refusal logprobs", func(r *schemas.BifrostResponse) {
			r.ChatResponse.Choices[0].LogProbs = &schemas.BifrostLogProbs{Refusal: []schemas.LogProb{{Token: "unsafe"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := response("safe")
			tc.mutate(r)
			got, bErr, err := p.PostLLMHook(primed(t, p, testContext()), r, nil)
			if got != nil || err != nil {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			requireCode(t, bErr, "content_safety_unsupported_content")
		})
	}
}

func TestTextBudgetAndEmptyText(t *testing.T) {
	p, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, matchingDetector)
	r := request(strings.Repeat("x", 256))
	r.ChatRequest.Input = append(r.ChatRequest.Input, message(strings.Repeat("x", 256)))
	_, short, err := p.PreLLMHook(testContext(), r)
	if err != nil || short == nil {
		t.Fatalf("short=%+v err=%v", short, err)
	}
	requireCode(t, short.Error, "content_safety_text_too_large")
	if _, short, err := p.PreLLMHook(testContext(), request("")); err != nil || short != nil {
		t.Fatal("explicit empty text rejected")
	}
	out, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	_, bErr, err := out.PostLLMHook(primed(t, out, testContext()), response(strings.Repeat("x", 257), strings.Repeat("x", 256)), nil)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, bErr, "content_safety_text_too_large")
	var seen []string
	separate, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, func(_ context.Context, text string) ([]guardrails.Finding, error) {
		seen = append(seen, text)
		return nil, nil
	})
	_, bErr, err = separate.PostLLMHook(primed(t, separate, testContext()), response("one", "two"), nil)
	if err != nil || bErr != nil || strings.Join(seen, "|") != "one|two" {
		t.Fatalf("seen=%v err=%v bErr=%v", seen, err, bErr)
	}
}

func TestExtractionChecksSizeBeforeUTF8(t *testing.T) {
	oversized := strings.Repeat("x", 512) + string([]byte{0xff})
	input, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Input)}, matchingDetector)
	_, short, err := input.PreLLMHook(testContext(), request(oversized))
	if err != nil || short == nil {
		t.Fatalf("short=%+v err=%v", short, err)
	}
	requireCode(t, short.Error, "content_safety_text_too_large")
	output, _ := newPlugin(t, []guardrails.Rule{testRule(guardrails.Output)}, matchingDetector)
	got, bErr, err := output.PostLLMHook(primed(t, output, testContext()), response(oversized), nil)
	if err != nil || got != nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	requireCode(t, bErr, "content_safety_text_too_large")
}
