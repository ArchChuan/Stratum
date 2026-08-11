package application

import "testing"

// factsCollectionName 必须与 pipeline 侧 memoryFactsCollectionName 命名一致：
// 模型名经 constants.SanitizeMilvusName 清洗（非字母数字下划线替换为下划线），
// Milvus 拒绝的字符（如 "."）不得进入 collection 名；model 为空时回退 legacy 名。
func TestFactsCollectionName(t *testing.T) {
	tests := []struct {
		name            string
		tenantID, model string
		want            string
	}{
		{name: "no model falls back to legacy", tenantID: "tenant-1", want: "memory_facts_tenant_1"},
		{name: "dashed model", tenantID: "t1", model: "text-embedding-v3", want: "memory_facts_t1_text_embedding_v3"},
		{name: "dotted model sanitized", tenantID: "t1", model: "text-embedding-v3.1", want: "memory_facts_t1_text_embedding_v3_1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := factsCollectionName(tc.tenantID, tc.model); got != tc.want {
				t.Errorf("factsCollectionName(%q, %q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
			}
		})
	}
}
