package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// TestResolveMaxOutputTokens 表驱动验证执行层兜底链：
// 显式 MaxTokens > 已解析 OutputReserve > 兜底常量（生产故障：
// max_tokens=0 被 omitempty 丢弃导致 provider 400）。
func TestResolveMaxOutputTokens(t *testing.T) {
	cases := []struct {
		name     string
		explicit int
		reserve  int
		want     int
	}{
		{"returns fallback constant when both zero", 0, 0, constants.DefaultOutputReserveTokens},
		{"returns explicit when reserve unset", 512, 0, 512},
		{"returns reserve when explicit unset", 0, 8000, 8000},
		{"explicit wins over reserve", 2048, 2048, 2048},
		{"returns reserve equal to constant", 0, 4096, 4096},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMaxOutputTokens(tc.explicit, tc.reserve)
			if got != tc.want {
				t.Fatalf("resolveMaxOutputTokens(%d, %d) = %d, want %d",
					tc.explicit, tc.reserve, got, tc.want)
			}
		})
	}
}
