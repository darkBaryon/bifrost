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

// 输入只检查最后一条消息。客户端会把被拦的消息留在历史里，检查全部历史会让之后每一条请求都被同一句话拦下；
// 历史里的每条消息在它作为最后一条到达时都已经检查过。这也是 Higress、Portkey、Azure 的默认做法。
func extractInputText(req *schemas.BifrostChatRequest, limit int) (string, error) {
	if len(req.Input) == 0 {
		return "", errUnsupported
	}
	var b strings.Builder
	if err := appendMessage(&b, req.Input[len(req.Input)-1], limit); err != nil {
		return "", err
	}
	return b.String(), nil
}

// 每个 choice 是独立候选回答，分别检测，防止拼接不相干回答改变语义；总体仍共享字节预算。
func extractOutputTexts(resp *schemas.BifrostResponse, limit int) ([]string, error) {
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
	// 先判剩余预算，再做线性的 UTF-8 扫描，避免超限片段消耗扫描成本。
	if !utf8.ValidString(text) {
		return errUnsupported
	}
	b.WriteString(text)
	return nil
}
