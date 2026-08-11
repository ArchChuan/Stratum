package infrastructure

import "testing"

func TestLookupModelSpec(t *testing.T) {
	cases := []struct {
		name  string // 用例名：行为描述
		model string // 查询的模型名
		want  int    // 期望 context window；0 = unknown
	}{
		{name: "exact match qwen-plus-latest", model: "qwen-plus-latest", want: 131072},
		{name: "exact case insensitive GPT-4o", model: "GPT-4o", want: 128000},
		// yi-lightning 精确条目 16384 与 yi- 族前缀 32768 不同——这两个用例能区分
		// "精确匹配"与"前缀回退"：精确路径（含大小写不敏感回退）被删时必然失败。
		{name: "exact beats prefix family", model: "yi-lightning", want: 16384},
		{name: "exact case insensitive beats prefix", model: "YI-Lightning", want: 16384},
		// qwen-max 精确条目 32768 与 qwen 族前缀 131072 不同，同样区分精确/前缀。
		{name: "exact qwen-max window beats prefix", model: "qwen-max", want: 32768},
		// 前缀族匹配：带版本/尺寸后缀的模型命中族窗口
		{name: "prefix family deepseek-v4-flash", model: "deepseek-v4-flash", want: 65536},
		{name: "prefix family qwen3-max-202508", model: "qwen3-max-202508", want: 131072},
		{name: "prefix family glm-5-air", model: "glm-5-air", want: 128000},
		// 未知模型
		{name: "unknown model", model: "x-unknown-9", want: 0},
		{name: "empty", model: "", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := LookupModelSpec(tc.model)
			if got != tc.want {
				t.Fatalf("LookupModelSpec(%q) window = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestLookupModelSpec_ReturnsMaxOutput(t *testing.T) {
	_, maxOut := LookupModelSpec("qwen-max")
	if maxOut != 8192 {
		t.Fatalf("qwen-max maxOut = %d, want 8192", maxOut)
	}
}
