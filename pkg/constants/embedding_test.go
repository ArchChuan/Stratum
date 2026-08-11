package constants

import "testing"

func TestDimensionForModel(t *testing.T) {
	tests := []struct {
		model string
		want  int
	}{
		{"text-embedding-v1", 1536},
		{"text-embedding-v2", 1024}, // 修正点：历史 1536 → 1024，与 knowledge 旧 vectorDim 一致
		{"text-embedding-v3", 1024},
		{"text-embedding-v4", 1024},
		{"embedding-3", 2048},
		{"text-embedding-3-small", 1536}, // default
		{"unknown-model", 1536},          // default
	}
	for _, tc := range tests {
		t.Run(tc.model, func(t *testing.T) {
			if got := DimensionForModel(tc.model); got != tc.want {
				t.Errorf("DimensionForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}
