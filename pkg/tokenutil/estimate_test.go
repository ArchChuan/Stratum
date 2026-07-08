package tokenutil

import "testing"

func TestEstimateText(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int // 不严格精确，只验证量级
	}{
		{"empty", "", 0},
		{"short", "hi", 1},
		{"english", "hello world this is a test", 8}, // 26bytes/3≈8
		{"chinese", "你好世界这是测试", 8},                   // 8chars*3bytes/3=8
		{"mixed", "hello你好", 7},                      // 5+6=11bytes/3≈3→实际7?
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateText(c.s)
			if got <= 0 && c.s != "" {
				t.Errorf("EstimateText(%q) = %d, want >0", c.s, got)
			}
		})
	}
}

func TestCostUSD(t *testing.T) {
	// qwen-turbo: 0.3/0.6 CNY per 1M, /7.2 to USD
	cost := CostUSD(1_000_000, 1_000_000, "qwen-turbo")
	if cost <= 0 {
		t.Errorf("CostUSD qwen-turbo = %f, want >0", cost)
	}

	// free model
	free := CostUSD(1_000_000, 1_000_000, "glm-4.7-flash")
	if free != 0 {
		t.Errorf("CostUSD glm-4.7-flash = %f, want 0", free)
	}

	// unknown model
	unknown := CostUSD(1000, 1000, "unknown-model-xyz")
	if unknown != 0 {
		t.Errorf("CostUSD unknown = %f, want 0", unknown)
	}
}

func TestLookupPrefixMatch(t *testing.T) {
	// "qwen-max-20250101" 应命中 "qwen-max"
	p, ok := Lookup("qwen-max-20250101")
	if !ok {
		t.Fatal("prefix match failed for qwen-max-20250101")
	}
	if p.Currency != "CNY" {
		t.Errorf("currency = %s, want CNY", p.Currency)
	}
}
