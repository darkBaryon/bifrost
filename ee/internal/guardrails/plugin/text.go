// 本文件提取有界的纯文本，拒绝本轮无法完整检查的载体，避免将未知结构当作空文本。
package plugin

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/maximhq/bifrost/core/schemas"
)

var errUnsupported = errors.New("unsupported chat content")

func unsupportedRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) bool {
	for _, key := range []schemas.BifrostContextKey{schemas.BifrostContextKeyUseRawRequestBody, schemas.BifrostContextKeyLargePayloadMode, schemas.BifrostContextKeyLargeResponseMode} {
		if enabled, _ := ctx.Value(key).(bool); enabled {
			return true
		}
	}
	// 大响应阈值可在 Provider 调用后才切换到 reader 透传，必须在此前拒绝。
	if threshold, _ := ctx.Value(schemas.BifrostContextKeyLargeResponseThreshold).(int64); threshold > 0 {
		return true
	}
	if len(req.RawRequestBody) > 0 {
		return true
	}
	if params := req.Params; params != nil {
		if len(params.ExtraParams) > 0 || len(params.Tools) > 0 || params.ToolChoice != nil || len(params.MCPServers) > 0 || params.Container != nil || len(params.ContextManagement) > 0 || params.Prediction != nil || params.ResponseFormat != nil || params.WebSearchOptions != nil || params.Audio != nil {
			return true
		}
		for _, modality := range params.Modalities {
			if modality != "text" {
				return true
			}
		}
	}
	return false
}

func inputText(req *schemas.BifrostChatRequest, limit int) (string, error) {
	if len(req.Input) == 0 {
		return "", errUnsupported
	}
	var b strings.Builder
	for i, message := range req.Input {
		if i > 0 {
			if err := appendText(&b, "\n", limit); err != nil {
				return "", err
			}
		}
		if err := appendMessage(&b, message, limit); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

// 每个 choice 是独立候选回答，分别检测，防止拼接不相干回答改变语义；总体仍共享字节预算。
func outputTexts(resp *schemas.BifrostResponse, limit int) ([]string, error) {
	if resp == nil || resp.ChatResponse == nil {
		return nil, errUnsupported
	}
	chat := resp.ChatResponse
	if len(chat.Choices) == 0 || len(chat.ExtraParams) > 0 || len(chat.SearchResults) > 0 || len(chat.Citations) > 0 || len(chat.Videos) > 0 || chat.ExtraFields.RawRequest != nil || chat.ExtraFields.RawResponse != nil {
		return nil, errUnsupported
	}
	texts := make([]string, 0, len(chat.Choices))
	remaining := limit
	for _, choice := range chat.Choices {
		if choice.LogProbs != nil || choice.ChatStreamResponseChoice != nil || choice.TextCompletionResponseChoice != nil || choice.ChatNonStreamResponseChoice == nil || choice.Message == nil || choice.StopString != nil {
			return nil, errUnsupported
		}
		var b strings.Builder
		if err := appendMessage(&b, *choice.Message, remaining); err != nil {
			return nil, err
		}
		remaining -= b.Len()
		texts = append(texts, b.String())
	}
	return texts, nil
}

func appendMessage(b *strings.Builder, message schemas.ChatMessage, limit int) error {
	if message.Name != nil || message.ChatToolMessage != nil || message.Role == schemas.ChatMessageRoleTool {
		return errUnsupported
	}
	if a := message.ChatAssistantMessage; a != nil {
		if a.Refusal != nil || a.Audio != nil || a.Reasoning != nil || len(a.ReasoningDetails) > 0 || len(a.Annotations) > 0 || len(a.ToolCalls) > 0 {
			return errUnsupported
		}
	}
	content := message.Content
	if content == nil {
		return errUnsupported
	}
	if content.ContentStr != nil {
		if content.ContentBlocks != nil {
			return errUnsupported
		}
		return appendText(b, *content.ContentStr, limit)
	}
	if content.ContentBlocks == nil {
		return errUnsupported
	}
	for _, block := range content.ContentBlocks {
		if block.Type != schemas.ChatContentBlockTypeText || block.Text == nil || block.Refusal != nil || block.ImageURLStruct != nil || block.InputAudio != nil || block.File != nil || block.Citations != nil || block.CachePoint != nil {
			return errUnsupported
		}
		if err := appendText(b, *block.Text, limit); err != nil {
			return err
		}
	}
	return nil
}

func appendText(b *strings.Builder, text string, limit int) error {
	if len(text) > limit-b.Len() {
		return guardrails.ErrTextTooLarge
	}
	if !utf8.ValidString(text) {
		return errUnsupported
	}
	b.WriteString(text)
	return nil
}
