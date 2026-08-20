package pipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
)

func TestMemoryCollectionNames(t *testing.T) {
	tests := []struct {
		tenantID, model, want string
	}{
		{"11111111-1111-1111-1111-111111111111", "text-embedding-v3",
			"memory_11111111_1111_1111_1111_111111111111_text_embedding_v3"},
		{"t1", "embedding-3", "memory_t1_embedding_3"},
		// 带横线租户 + 带点模型：与 persistence 侧 legacy 名 pin 相同的契约输入
		// （persistence/milvus_adapter_test.go TestLegacyCollectionNamesPin）。
		{"my-tenant-42", "text-embedding-v3.1", "memory_my_tenant_42_text_embedding_v3_1"},
	}
	for _, tc := range tests {
		if got := memoryCollectionName(tc.tenantID, tc.model); got != tc.want {
			t.Errorf("memoryCollectionName(%q,%q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
		}
	}
	if got := memoryCollectionLegacyName("t1"); got != "memory_t1" {
		t.Errorf("legacy name = %q, want memory_t1", got)
	}
	if got := memoryCollectionLegacyName("my-tenant-42"); got != "memory_my_tenant_42" {
		t.Errorf("legacy name = %q, want memory_my_tenant_42", got)
	}
}

// TestMemoryFactsCollectionNames pins the facts-side naming (write + legacy)
// so a future rename is caught by the suite, not by a prod incident.
func TestMemoryFactsCollectionNames(t *testing.T) {
	tests := []struct {
		tenantID, model, want string
	}{
		{"11111111-1111-1111-1111-111111111111", "text-embedding-v3",
			"memory_facts_11111111_1111_1111_1111_111111111111_text_embedding_v3"},
		{"t1", "embedding-3", "memory_facts_t1_embedding_3"},
		// 带横线租户 + 带点模型：与 persistence 侧 legacy 名 pin 相同的契约输入
		// （persistence/milvus_adapter_test.go TestLegacyCollectionNamesPin）。
		{"my-tenant-42", "text-embedding-v3.1", "memory_facts_my_tenant_42_text_embedding_v3_1"},
	}
	for _, tc := range tests {
		if got := memoryFactsCollectionName(tc.tenantID, tc.model); got != tc.want {
			t.Errorf("memoryFactsCollectionName(%q,%q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
		}
	}
	if got := memoryFactsCollectionLegacyName("t1"); got != "memory_facts_t1" {
		t.Errorf("facts legacy name = %q, want memory_facts_t1", got)
	}
	if got := memoryFactsCollectionLegacyName("my-tenant-42"); got != "memory_facts_my_tenant_42" {
		t.Errorf("facts legacy name = %q, want memory_facts_my_tenant_42", got)
	}
}

func TestEmbeddingServiceExposesModel(t *testing.T) {
	svc := embedding.NewEmbeddingServiceWithModel(nil, "text-embedding-v3", nil)
	if got := svc.Model(); got != "text-embedding-v3" {
		t.Errorf("Model() = %q", got)
	}
}

// TestEnsureCollectionFailClosedWhenNoDim pins the fail-closed gate in
// ensureCollection: when the tenant's explicit embedding model cannot be
// resolved to a dimension (dim ≤ 0), no collection is created and the error
// propagates so the upstream Upsert fails → message goes to DLQ. A nil
// VectorStore is safe here precisely because the error is returned before
// the store is ever touched — proving we never fall back to a default
// dimension and create a mis-shaped collection.
func TestEnsureCollectionFailClosedWhenNoDim(t *testing.T) {
	tests := []struct {
		name string
		dim  int
	}{
		{name: "dim zero", dim: 0},
		{name: "dim negative", dim: -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewMilvusVectorAdapter(nil).WithDimResolver(func(context.Context, string) int {
				return tc.dim
			})
			err := a.ensureCollection(context.Background(), "t1", "embedding-3")
			require.Error(t, err)
			require.Contains(t, err.Error(), "no embedding model configured")
			// fail-closed 路径绝不把集合标记为已创建。
			_, marked := a.ensured.Load("t1/embedding-3")
			require.False(t, marked)
		})
	}
}
