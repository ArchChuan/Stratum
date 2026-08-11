package pipeline

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
)

func TestMemoryCollectionNames(t *testing.T) {
	tests := []struct {
		tenantID, model, want string
	}{
		{"11111111-1111-1111-1111-111111111111", "text-embedding-v3",
			"memory_11111111_1111_1111_1111_111111111111_text_embedding_v3"},
		{"t1", "embedding-3", "memory_t1_embedding_3"},
	}
	for _, tc := range tests {
		if got := memoryCollectionName(tc.tenantID, tc.model); got != tc.want {
			t.Errorf("memoryCollectionName(%q,%q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
		}
	}
	if got := memoryCollectionLegacyName("t1"); got != "memory_t1" {
		t.Errorf("legacy name = %q, want memory_t1", got)
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
	}
	for _, tc := range tests {
		if got := memoryFactsCollectionName(tc.tenantID, tc.model); got != tc.want {
			t.Errorf("memoryFactsCollectionName(%q,%q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
		}
	}
	if got := memoryFactsCollectionLegacyName("t1"); got != "memory_facts_t1" {
		t.Errorf("facts legacy name = %q, want memory_facts_t1", got)
	}
}

func TestEmbeddingServiceExposesModel(t *testing.T) {
	svc := embedding.NewEmbeddingServiceWithModel(nil, "text-embedding-v3", nil)
	if got := svc.Model(); got != "text-embedding-v3" {
		t.Errorf("Model() = %q", got)
	}
}
