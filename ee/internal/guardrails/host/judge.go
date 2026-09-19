// Package host 把内容安全模块接到内嵌的 Bifrost：判官通过网关自身发内部子请求，配置经 Builder 变成检查器。
// 本文件实现判官模型适配器。
package host

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
)

// Client 是判官需要的网关能力：一次非流式 Chat 调用。由 host.Client(*bifrost.Bifrost) 满足。
type Client interface {
	ChatCompletionRequest(ctx *schemas.BifrostContext, req *schemas.BifrostChatRequest) (*schemas.BifrostChatResponse, *schemas.BifrostError)
}

// Logger 是本包需要的宿主日志能力。
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}

// 判官调用参数：温度 0 保证判定稳定，输出只需一个小 JSON。
const (
	judgeTemperature         = 0.0
	judgeMaxCompletionTokens = 200
)

// JudgeModel 通过网关调用配置的判官 provider/model；每次调用都是跳过插件管线的内部子请求。
type JudgeModel struct {
	client   Client
	provider schemas.ModelProvider
	model    string
	log      Logger
}

// NewJudgeModel 构造适配器；provider 是否存在由 Builder 事先校验。
func NewJudgeModel(client Client, provider, model string, log Logger) *JudgeModel {
	return &JudgeModel{client: client, provider: schemas.ModelProvider(provider), model: model, log: log}
}

// Complete 发送 system+user 两条消息并返回助手正文。
// 派生的 BifrostContext 继承 ctx 的期限与取消，并清掉调用方的 key 路由与传输状态，判官不会再经过本插件。
func (m *JudgeModel) Complete(ctx context.Context, system, user string) (string, error) {
	deadline := schemas.NoDeadline
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	chatCtx := schemas.NewBifrostContext(ctx, deadline)
	// NewBifrostContext 启动的取消监视 goroutine 持有 ctx；调用结束后必须取消，避免它比本次调用活得更久。
	defer chatCtx.Cancel()
	bifrost.PrepareContextForInternalRequest(chatCtx)

	temperature := judgeTemperature
	maxTokens := judgeMaxCompletionTokens
	var responseFormat interface{} = map[string]any{"type": "json_object"}
	req := &schemas.BifrostChatRequest{
		Provider: m.provider,
		Model:    m.model,
		Input: []schemas.ChatMessage{
			{Role: schemas.ChatMessageRoleSystem, Content: &schemas.ChatMessageContent{ContentStr: &system}},
			{Role: schemas.ChatMessageRoleUser, Content: &schemas.ChatMessageContent{ContentStr: &user}},
		},
		Params: &schemas.ChatParameters{Temperature: &temperature, MaxCompletionTokens: &maxTokens, ResponseFormat: &responseFormat},
	}
	started := time.Now()
	resp, bErr := m.client.ChatCompletionRequest(chatCtx, req)
	if bErr != nil {
		err := judgeError(bErr)
		m.log.Info("content safety judge: provider=%s model=%s ok=false failure=%q elapsed=%s", m.provider, m.model, err, time.Since(started))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", err
	}
	text, err := responseText(resp)
	usage := "n/a"
	if resp != nil && resp.Usage != nil {
		usage = fmt.Sprintf("%d/%d", resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	}
	m.log.Info("content safety judge: provider=%s model=%s ok=%t tokens=%s elapsed=%s", m.provider, m.model, err == nil, usage, time.Since(started))
	return text, err
}

// 网关自身产生的错误（IsBifrostError，如"没有支持该模型的 key"、请求取消）的类型、代码与消息前 maxErrorMessageBytes 字节
// 保留用于排障；provider 返回的错误字段（type、code、message）都可能回显送检内容，一律不进日志与错误链，
// 只按 HTTP 状态码归入本地白名单类别。
const maxErrorMessageBytes = 200

// judgeError 把网关错误转成不含送检内容的普通 error。
func judgeError(bErr *schemas.BifrostError) error {
	parts := []string{"judge request failed"}
	if bErr.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *bErr.StatusCode))
	}
	if bErr.IsBifrostError && bErr.Error != nil {
		if bErr.Error.Type != nil {
			parts = append(parts, "type="+*bErr.Error.Type)
		}
		if bErr.Error.Code != nil {
			parts = append(parts, "code="+*bErr.Error.Code)
		}
		if msg := truncateUTF8(strings.TrimSpace(bErr.Error.Message), maxErrorMessageBytes); msg != "" {
			parts = append(parts, "message="+strconv.Quote(msg))
		}
	} else {
		parts = append(parts, "category="+providerFailureCategory(bErr.StatusCode))
	}
	return errors.New(strings.Join(parts, " "))
}

// providerFailureCategory 只按状态码给出固定类别，不引用 provider 的任何自由文本。
func providerFailureCategory(status *int) string {
	if status == nil {
		return "provider_error"
	}
	switch code := *status; {
	case code == 401 || code == 403:
		return "provider_auth"
	case code == 429:
		return "provider_rate_limited"
	case code == 499:
		return "cancelled"
	case code >= 500:
		return "provider_unavailable"
	case code >= 400:
		return "provider_rejected"
	}
	return "provider_error"
}

// truncateUTF8 按字节上限截断但不切断多字节字符。
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// responseText 取第一个候选的纯文本；没有文本视为一次失败（可重试）。
func responseText(resp *schemas.BifrostChatResponse) (string, error) {
	if resp == nil || len(resp.Choices) == 0 || resp.Choices[0].ChatNonStreamResponseChoice == nil || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content == nil {
		return "", errors.New("judge response has no message")
	}
	content := resp.Choices[0].Message.Content
	if content.ContentStr != nil {
		return *content.ContentStr, nil
	}
	var b strings.Builder
	for _, block := range content.ContentBlocks {
		if block.Type == schemas.ChatContentBlockTypeText && block.Text != nil {
			b.WriteString(*block.Text)
		}
	}
	if b.Len() == 0 {
		return "", errors.New("judge response has no text")
	}
	return b.String(), nil
}
