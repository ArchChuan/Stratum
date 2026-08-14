package infrastructure

import (
	"strings"

	"go.uber.org/zap"
)

const zhipuBaseURL = "https://open.bigmodel.cn/api/paas/v4"

var zhipuChatModels = []string{
	"glm-5.2",
	"glm-4.7-flashx", "glm-4.7-flash", "glm-4.5-flash",
	"glm-4-plus", "glm-4", "glm-4-air", "glm-4-flash", "glm-4v",
}

// zhipuModelCatalog 智谱公开模型目录。GET /models 只返回 key 已开通的 8 个文本
// 对话模型，视觉/嵌入/推理等实际可调用（实测 chat/embeddings 接口 200）的模型
// 不在列表；发现时合并兜底，让实际可调用模型全部可见。
// 排除实测 400 的 glm-4.1v（模型 id 不存在）。目录是展示兜底，实际可用性仍
// 取决于账号权限（如 glm-z1 需单独开通）。
var zhipuModelCatalog = []string{
	// 文本（与 /models 已返回的模型重叠，去重无害）
	"glm-4.5", "glm-4.5-air", "glm-4.6", "glm-4.7", "glm-5", "glm-5-turbo", "glm-5.1", "glm-5.2",
	// 旧文本
	"glm-4", "glm-4-air", "glm-4-plus", "glm-4-flash", "glm-4.5-flash", "glm-4.7-flash",
	// 推理
	"glm-z1-air", "glm-z1",
	// 视觉
	"glm-4v-plus", "glm-4v-flash", "glm-4.5v", "glm-4.6v", "glm-4.6v-flash", "glm-5v-turbo",
	// 嵌入
	"embedding-2", "embedding-3",
}

// ZhipuModelCatalog 返回 baseURL 指向智谱（bigmodel.cn）时的发现兜底目录副本；
// 非智谱 baseURL 返回 nil（行为不变）。硬编码控制逻辑集中于此，供
// ProviderRuntime.protocol 按 provider 注入。
func ZhipuModelCatalog(baseURL string) []string {
	if strings.Contains(baseURL, "bigmodel.cn") {
		return append([]string(nil), zhipuModelCatalog...)
	}
	return nil
}

type ZhipuClient = OpenAICompatClient

func NewZhipuClient(apiKey string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     zhipuBaseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      append([]string(nil), zhipuChatModels...),
	}, logger)
}

func NewZhipuClientWithBase(apiKey, baseURL string, logger *zap.Logger) *ZhipuClient {
	return NewOpenAICompatClient(ProviderConfig{
		Name:        "zhipu",
		BaseURL:     baseURL,
		APIKey:      apiKey,
		HealthModel: "glm-4-flash",
		Models:      append([]string(nil), zhipuChatModels...),
	}, logger)
}
