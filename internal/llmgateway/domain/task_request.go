package domain

import (
	"math"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// jsonObjectType 是 OpenAI-compatible JSON mode 的 response_format type。
const jsonObjectType = "json_object"

// JSONObject 返回 OpenAI-compatible JSON mode 的 response_format。
// provider 保证返回合法 JSON，服务端校验退化为语义层；不支持 json_object 的
// 模型由 gateway applyCapabilityGate 能力门控清空（fail-closed），
// 请求侧可无条件设置。
func JSONObject() *ResponseFormat {
	return &ResponseFormat{Type: jsonObjectType}
}

// NewChatRequest 原样透传构造通用对话请求（agent 现行链路可选接入，本次不强制）。
// 不设 temperature / response_format / NoPrimaryRetry：透传语义，零任务默认值。
func NewChatRequest(model string, msgs []Message, tools []Tool, effort string) *CompletionRequest {
	return &CompletionRequest{
		Model:           model,
		Messages:        msgs,
		Tools:           tools,
		ReasoningEffort: effort,
	}
}

// RoundTemperature 把温度收敛到 2 位小数：智谱等 OpenAI 兼容端点在请求体
// 校验小数点位数（超过 2 位返回 400）。float32→float64 直转会把 0.1 变成
// 0.10000000149011612，必须先舍入再发送，避免 provider 契约 400。
func RoundTemperature(v float64) float64 {
	return math.Round(v*100) / 100
}

// PlatformTemperaturePtr 把调用方的 float32 温度转成 *float64：0 = unset
// （agent 语义贯穿全链路）→ nil，让网关采样注入层生效；非 0 → 显式值
// （2 位小数舍入）。平台参数覆盖点必须复用本函数，禁止 float64(float32)
// 直转绕过舍入——智谱等端点校验小数位数，超 2 位返回 400。
func PlatformTemperaturePtr(v float32) *float64 {
	if v == 0 {
		return nil
	}
	f := RoundTemperature(float64(v))
	return &f
}

// NewSummarizeRequest 构造单轮总结请求：
//   - 单条 user 消息 = instructions + items（\n 连接，items 可为 nil）；
//   - Temperature = TaskSummarizeTemperature（低温度稳定压缩）；
//   - MaxTokens = 参数传入（调用方锁输出长度，0 = 不锁）；
//   - 无 tools；
//   - NoPrimaryRetry = true：压缩路径语义——时间片内主模型一次失败直接降级候选，
//     不消耗主模型重试预算。
func NewSummarizeRequest(model, instructions string, items []string, maxTokens int) *CompletionRequest {
	content := instructions
	if len(items) > 0 {
		content += strings.Join(items, "\n")
	}
	return &CompletionRequest{
		Model:          model,
		Messages:       []Message{{Role: "user", Content: content}},
		Temperature:    PlatformTemperaturePtr(constants.TaskSummarizeTemperature),
		MaxTokens:      maxTokens,
		NoPrimaryRetry: true,
	}
}

// NewExtractRequest 构造结构化抽取请求：
//   - system != "" 时消息为 [system, user]，否则只有 user；
//   - ResponseFormat = json_object（能力门控在 gateway 兜底）；
//   - temperature / maxTokens 显式参数，消费方传各自 pkg/constants 常量。
func NewExtractRequest(model, system, user string, temperature float32, maxTokens int) *CompletionRequest {
	msgs := make([]Message, 0, 2)
	if system != "" {
		msgs = append(msgs, Message{Role: "system", Content: system})
	}
	msgs = append(msgs, Message{Role: "user", Content: user})
	return &CompletionRequest{
		Model:          model,
		Messages:       msgs,
		Temperature:    PlatformTemperaturePtr(temperature),
		MaxTokens:      maxTokens,
		ResponseFormat: JSONObject(),
	}
}
