package application

import (
	"fmt"
	"strings"
)

// factsCollectionName builds the Milvus collection name for LLM-extracted
// facts. 模型后缀编码嵌入模型，换模型时数据隔离到新 collection；model 为空
// （无可用嵌入模型）时回退无后缀的 legacy 名（升级前存量数据）。
func factsCollectionName(tenantID, model string) string {
	if model == "" {
		return fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(tenantID, "-", "_"))
	}
	return fmt.Sprintf("memory_facts_%s_%s", strings.ReplaceAll(tenantID, "-", "_"),
		strings.ReplaceAll(model, "-", "_"))
}
