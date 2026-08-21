package application

import (
	"reflect"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// TestInferCapabilities 覆盖发现时的能力推断：嵌入族独占 CapEmbedding；
// 其余 chat 模型默认 CapChat+CapToolUse，多模态/推理族按命名规则追加
// CapVision/CapReasoning。命名匹配是启发式，表驱动用例即行为契约。
func TestInferCapabilities(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  []domain.ModelCapability
	}{
		{name: "embed 子串", model: "text-embedding-3-small", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "embed 大小写不敏感", model: "text-Embedding-3-large", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "bge 嵌入族", model: "bge-m3", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "m3e 嵌入族", model: "m3e-base", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "e5 嵌入族", model: "e5-large-v2", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "gte 嵌入族", model: "gte-large-en-v1.5", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "text2vec 嵌入族", model: "text2vec-large-chinese", want: []domain.ModelCapability{domain.CapEmbedding}},
		{name: "纯 chat", model: "deepseek-chat", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse}},
		{name: "旧模型纯 chat", model: "gpt-3.5-turbo", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse}},
		{name: "gpt-4o 多模态", model: "gpt-4o", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "gpt-4o-mini 多模态", model: "gpt-4o-mini", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "gpt-4.1 多模态", model: "gpt-4.1-nano", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "claude-3 多模态", model: "claude-3-5-sonnet", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "claude-4 多模态", model: "claude-sonnet-4-5", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "qwen-vl 多模态", model: "qwen-vl-max", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "qwen2-vl 多模态", model: "qwen2-vl-72b", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "glm-4v 多模态", model: "glm-4v-plus", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "glm-4.5v 多模态", model: "glm-4.5v", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "glm-4.6v 多模态", model: "glm-4.6v", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "glm-5v 多模态", model: "glm-5v-turbo", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "gemini 多模态", model: "gemini-1.5-pro", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "llava 多模态", model: "llava-v1.6-34b", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "internvl 多模态", model: "internvl2-8b", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
		{name: "o 系推理", model: "o3-mini", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapReasoning}},
		{name: "deepseek-reasoner 推理", model: "deepseek-reasoner", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapReasoning}},
		{name: "qwq 推理", model: "qwq-32b", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapReasoning}},
		{name: "glm-z1 推理", model: "glm-z1-air", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapReasoning}},
		{name: "大写混合", model: "GPT-4O", want: []domain.ModelCapability{domain.CapChat, domain.CapToolUse, domain.CapVision}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := inferCapabilities(tc.model)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("inferCapabilities(%q) = %v, want %v", tc.model, got, tc.want)
			}
		})
	}
}
