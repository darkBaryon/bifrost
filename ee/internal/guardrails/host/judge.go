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

// 错误消息保留上限，按 UTF-8 边界截断。
const maxErrorMessageBytes = 200

// 上游在拿不到可用 key 或连不上 provider 时合成的错误类型（IsBifrostError 为 false、带状态码）：
// 连接失败 schemas.ProviderConnectionFailed（502），key 全部失效 upstream_credentials_exhausted（502），
// key 全被过滤 no_eligible_keys（503；本仓未装配 KeyPoolFilter，当前不可达，一并收录以防日后装配后静默丢失诊断）。
// 出处 core/providers/utils/utils.go NewBifrostUpstreamConnectionError 与 core/bifrost.go executeRequestWithRetries。
var synthesizedGatewayTypes = map[string]bool{
	schemas.ProviderConnectionFailed: true,
	"upstream_credentials_exhausted": true,
	"no_eligible_keys":               true,
}

// judgeError 把网关错误转成不含送检内容的普通 error。类别名只陈述可验证的事实：来源只在能从网关自己的标记推出时才写
// （gateway_internal），按状态码派生的类别不断言来源：
//   - IsBifrostError 为 true 或没有状态码：文本由网关或传输层生成（取消、超时、key 池没有支持判官模型的 key），
//     provider 正文无法影响这些字段，记 category=gateway_internal 并保留类型、代码与截断后的消息；
//   - 带状态码：只按状态码归入 http_* 类别（状态码可能来自 provider 响应，也可能是网关合成的 502/503）；
//     若类型命中 synthesizedGatewayTypes，附加 known_type=<本地清单常量>（多半是网关合成的连接失败 / key 耗尽，
//     但 Error.Type 在 provider 响应路径上由 provider 正文原样填入、不可信，所以只输出比对命中的常量名，不复制 code/message）。
func judgeError(bErr *schemas.BifrostError) error {
	parts := []string{"judge request failed"}
	if bErr.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status=%d", *bErr.StatusCode))
	}
	if bErr.IsBifrostError || bErr.StatusCode == nil {
		parts = append(parts, "category=gateway_internal")
		if bErr.Error != nil {
			if bErr.Error.Type != nil {
				parts = append(parts, "type="+*bErr.Error.Type)
			}
			if bErr.Error.Code != nil {
				parts = append(parts, "code="+*bErr.Error.Code)
			}
			if msg := truncateUTF8(strings.TrimSpace(bErr.Error.Message), maxErrorMessageBytes); msg != "" {
				parts = append(parts, "message="+strconv.Quote(msg))
			}
		}
		return errors.New(strings.Join(parts, " "))
	}
	parts = append(parts, "category="+statusCategory(*bErr.StatusCode))
	if bErr.Error != nil && bErr.Error.Type != nil && synthesizedGatewayTypes[*bErr.Error.Type] {
		parts = append(parts, "known_type="+*bErr.Error.Type)
	}
	return errors.New(strings.Join(parts, " "))
}

// statusCategory 只按 HTTP 状态码给出固定类别，不看状态码由谁产生，也不引用任何自由文本。
// 数字是 HTTP 状态码；本包不引入 HTTP 框架，故不用命名常量。
func statusCategory(status int) string {
	switch {
	case status == 401 || status == 403:
		return "http_auth"
	case status == 429:
		return "http_rate_limited"
	case status == 499:
		return "http_cancelled"
	case status >= 500:
		return "http_unavailable"
	case status >= 400:
		return "http_rejected"
	}
	return "http_other"
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
