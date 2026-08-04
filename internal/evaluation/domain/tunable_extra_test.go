package domain

import (
	"strings"
	"testing"
)

// tunablesUnderTest 返回需要全覆盖的 tunable 实例与期望元数据。
func tunablesUnderTest() []struct {
	name     string
	tunable  Tunable
	valid    any
	invalid  []any
	defaultV any
} {
	return []struct {
		name     string
		tunable  Tunable
		valid    any
		invalid  []any
		defaultV any
	}{
		{name: "temperature", tunable: temperatureTunable{}, valid: 0.7, invalid: []any{"hot", -1.0, 2.5}, defaultV: 0.7},
		{name: "max_tokens", tunable: maxTokensTunable{}, valid: 4096.0, invalid: []any{"many", -1.0, 200000.0}, defaultV: 4096},
	}
}

func TestTunableMetadata(t *testing.T) {
	for _, tc := range tunablesUnderTest() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.tunable.Key() == "" {
				t.Fatal("key must be non-empty")
			}
			if tc.tunable.DisplayName() == "" {
				t.Fatal("display name must be non-empty")
			}
			if tc.tunable.Category() == "" {
				t.Fatal("category must be set")
			}
			if tc.tunable.VisualHint().Control == "" {
				t.Fatal("visual hint control must be set")
			}
			if tc.tunable.DefaultValue() != tc.defaultV {
				t.Fatalf("default = %v, want %v", tc.tunable.DefaultValue(), tc.defaultV)
			}
			if len(AllTunableCategories) != 7 {
				t.Fatalf("category count = %d", len(AllTunableCategories))
			}
		})
	}
}

func TestTemperatureAndMaxTokensReadDefaults(t *testing.T) {
	temp, maxTok := temperatureTunable{}, maxTokensTunable{}
	// 极端情况：无 model_parameters → 默认值。
	if got, err := temp.Read(map[string]any{}); err != nil || got != 0.7 {
		t.Fatalf("read empty = %v, %v", got, err)
	}
	if got, err := maxTok.Read(map[string]any{}); err != nil || got != 4096 {
		t.Fatalf("read empty = %v, %v", got, err)
	}
	// 极端情况：model_parameters 存在但缺字段 → 默认值。
	resource := map[string]any{"model_parameters": map[string]any{"other": 1}}
	if got, err := temp.Read(resource); err != nil || got != 0.7 {
		t.Fatalf("read missing key = %v, %v", got, err)
	}
	// 命中字段。
	resource["model_parameters"] = map[string]any{"temperature": 0.9, "max_tokens": 8192.0}
	if got, _ := temp.Read(resource); got != 0.9 {
		t.Fatalf("read = %v", got)
	}
	if got, _ := maxTok.Read(resource); got != 8192.0 {
		t.Fatalf("read = %v", got)
	}
}

func TestTemperatureAndMaxTokensWrite(t *testing.T) {
	temp, maxTok := temperatureTunable{}, maxTokensTunable{}
	// 正常：写入已有 params。
	resource := map[string]any{"model_parameters": map[string]any{}}
	if err := temp.Write(resource, 1.5); err != nil {
		t.Fatalf("write = %v", err)
	}
	if resource["model_parameters"].(map[string]any)["temperature"] != 1.5 {
		t.Fatal("temperature not written")
	}
	// 极端情况：无 model_parameters → 创建后写入。
	resource = map[string]any{}
	if err := maxTok.Write(resource, 2048.0); err != nil {
		t.Fatalf("write = %v", err)
	}
	if resource["model_parameters"].(map[string]any)["max_tokens"] != 2048.0 {
		t.Fatal("max_tokens not written")
	}
	// 极端情况：类型错误 → 报错且不写入。
	for _, tt := range []Tunable{temp, maxTok} {
		if err := tt.Write(map[string]any{}, "not-a-number"); err == nil {
			t.Fatal("non-float write must fail")
		}
	}
}

func TestTemperatureAndMaxTokensValidate(t *testing.T) {
	cases := []struct {
		tunable Tunable
		good    []any
		bad     []any
	}{
		{temperatureTunable{}, []any{0.0, 1.0, 2.0}, []any{"x", -0.1, 2.1, nil}},
		{maxTokensTunable{}, []any{0.0, 256.0, 65536.0, 131072.0}, []any{"x", -0.1, 131073.0, nil}},
	}
	for _, tc := range cases {
		for _, v := range tc.good {
			if err := tc.tunable.Validate(v); err != nil {
				t.Fatalf("validate %v = %v", v, err)
			}
		}
		for _, v := range tc.bad {
			if err := tc.tunable.Validate(v); err == nil {
				t.Fatalf("validate %v must fail", v)
			}
		}
	}
}

func TestTunableSearchSpaces(t *testing.T) {
	// 数值 tunable 有网格；prompt tunable 走 LLM 驱动，网格为空。
	temp, maxTok, prompt := temperatureTunable{}, maxTokensTunable{}, promptTunable{}
	if got := temp.SearchSpace(); got.Min != 0 || got.Max != 2 || got.Step != 0.1 {
		t.Fatalf("temperature space = %+v", got)
	}
	if got := maxTok.SearchSpace(); got.Min != 0 || got.Max != 131072 {
		t.Fatalf("max_tokens space = %+v", got)
	}
	if got := prompt.SearchSpace(); got.Min != 0 || got.Max != 0 || got.Discrete != nil {
		t.Fatalf("prompt space = %+v", got)
	}
}

func TestPromptTunableReadWriteValidate(t *testing.T) {
	p := promptTunable{key: "system_prompt", displayName: "系统提示词", fieldPath: "system_prompt"}

	// 极端情况：缺字段 → 空串。
	if got, err := p.Read(map[string]any{}); err != nil || got != "" {
		t.Fatalf("read empty = %v, %v", got, err)
	}
	// 正常读取（含非 string 的兜底空串）。
	resource := map[string]any{"system_prompt": "be concise"}
	if got, _ := p.Read(resource); got != "be concise" {
		t.Fatalf("read = %v", got)
	}
	if got, _ := p.Read(map[string]any{"system_prompt": 42}); got != "" {
		t.Fatalf("read non-string = %v", got)
	}
	// 写入。
	if err := p.Write(resource, "new prompt"); err != nil {
		t.Fatalf("write = %v", err)
	}
	if resource["system_prompt"] != "new prompt" {
		t.Fatal("write not applied")
	}
	// 极端情况：非 string 写入失败；空串校验失败。
	if err := p.Write(resource, 5); err == nil {
		t.Fatal("non-string write must fail")
	}
	if err := p.Validate(""); err == nil {
		t.Fatal("empty prompt must fail validation")
	}
	if err := p.Validate("ok"); err != nil {
		t.Fatalf("validate = %v", err)
	}
	if err := p.Validate(7); err == nil {
		t.Fatal("non-string validate must fail")
	}
}

func TestResourceTunableCategoriesMap(t *testing.T) {
	agentCats := ResourceTunableCategories[ResourceKindAgent]
	if len(agentCats) != 7 {
		t.Fatalf("agent categories = %v", agentCats)
	}
	for _, c := range agentCats {
		found := false
		for _, all := range AllTunableCategories {
			if c == all {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("category %q not in AllTunableCategories", c)
		}
	}
	if _, ok := ResourceTunableCategories[ResourceKindSkill]; ok {
		t.Fatal("skill must not be explicitly listed (defaults to model_config only)")
	}
}

func TestSanitizeSafeSummaryValueExtremes(t *testing.T) {
	// 极端情况：nil 是安全的。
	if got, ok := sanitizeSafeSummaryValue(nil, 0); !ok || got != nil {
		t.Fatalf("nil = %v, %v", got, ok)
	}
	// 极端情况：深度超过上限 → 不安全。
	if _, ok := sanitizeSafeSummaryValue(1, 7); ok {
		t.Fatal("depth over limit must be unsafe")
	}
	// 极端情况：超长字符串 → 不安全。
	if _, ok := sanitizeSafeSummaryValue(strings.Repeat("x", 2049), 0); ok {
		t.Fatal("oversized string must be unsafe")
	}
	// 极端情况：超大 slice → 不安全。
	big := make([]any, 65)
	if _, ok := sanitizeSafeSummaryValue(big, 0); ok {
		t.Fatal("oversized slice must be unsafe")
	}
	// 嵌套递归：深层合法值通过。
	nested := []any{map[string]any{"a": []any{1, 2.5, "ok"}}}
	if _, ok := sanitizeSafeSummaryValue(nested, 0); !ok {
		t.Fatal("nested safe value must pass")
	}
	// 深层超限：第 7 层 → 不安全。
	deep := nested
	for i := 0; i < 6; i++ {
		deep = []any{deep}
	}
	if _, ok := sanitizeSafeSummaryValue(deep, 0); ok {
		t.Fatal("depth 7 must be unsafe")
	}
	// slice 内某项不安全 → 整体不安全。
	if _, ok := sanitizeSafeSummaryValue([]any{1, strings.Repeat("x", 5000)}, 0); ok {
		t.Fatal("slice with unsafe item must be unsafe")
	}
}
