// 本文件固定已审阅的上游字段集合，让上游升级触发文本提取边界复审。
package plugin_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestReviewedChatSchema(t *testing.T) {
	for _, tc := range []struct {
		value  any
		fields string
	}{
		{schemas.BifrostChatRequest{}, "Provider Model Input Params Fallbacks RawRequestBody"},
		{schemas.ChatParameters{}, "Audio FrequencyPenalty LogitBias LogProbs MaxCompletionTokens Metadata Modalities N ParallelToolCalls Prediction PresencePenalty PromptCacheKey PromptCacheRetention PromptCacheOptions Reasoning ResponseFormat SafetyIdentifier Seed ServiceTier StreamOptions Stop Store Temperature TopLogProbs TopP ToolChoice Tools User Verbosity WebSearchOptions TopK Speed InferenceGeo MCPServers Container CacheControl TaskBudget ContextManagement IncludeServerSideToolInvocations ExtraParams"},
		{schemas.ChatMessage{}, "Name Role Content ChatToolMessage ChatAssistantMessage"},
		{schemas.ChatMessageContent{}, "ContentStr ContentBlocks"},
		{schemas.ChatContentBlock{}, "Type Text Refusal ImageURLStruct InputAudio File CacheControl Citations PromptCacheBreakpoint CachePoint"},
		{schemas.ChatAssistantMessage{}, "Refusal Audio Reasoning ReasoningDetails Annotations ToolCalls"},
		{schemas.BifrostChatResponse{}, "ID Choices Created Model Object ServiceTier Speed InferenceGeo Diagnostics SystemFingerprint Usage ExtraFields ExtraParams SearchResults Videos Citations"},
		{schemas.BifrostResponseChoice{}, "Index FinishReason LogProbs TextCompletionResponseChoice ChatNonStreamResponseChoice ChatStreamResponseChoice"},
		{schemas.ChatNonStreamResponseChoice{}, "Message StopString"},
		{schemas.ChatReasoning{}, "Enabled Effort MaxTokens Display"},
		{schemas.CacheControl{}, "Type TTL Scope"},
	} {
		typ := reflect.TypeOf(tc.value)
		t.Run(typ.Name(), func(t *testing.T) {
			actual := make([]string, typ.NumField())
			for i := range actual {
				actual[i] = typ.Field(i).Name
			}
			if !slices.Equal(actual, strings.Fields(tc.fields)) {
				t.Fatalf("上游结构已变化，请先复审 text.go 的提取/拒绝判定，再更新字段清单：%v", actual)
			}
		})
	}
}
