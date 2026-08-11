package application

import (
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// factsCollectionName builds the Milvus collection name for LLM-extracted
// facts. 模型后缀编码嵌入模型，换模型时数据隔离到新 collection；model 为空
// （无可用嵌入模型）时回退无后缀的 legacy 名（升级前存量数据）。模型名经
// constants.SanitizeMilvusName 清洗（非字母数字下划线统一替换为下划线，
// 如 "text-embedding-v3.1" → "text_embedding_v3_1"），与 pipeline 侧
// memoryFactsCollectionName 命名完全一致，Milvus 拒绝的字符不会进入 collection 名。
func factsCollectionName(tenantID, model string) string {
	if model == "" {
		return fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(tenantID, "-", "_"))
	}
	return fmt.Sprintf("memory_facts_%s_%s", strings.ReplaceAll(tenantID, "-", "_"),
		constants.SanitizeMilvusName(model))
}
