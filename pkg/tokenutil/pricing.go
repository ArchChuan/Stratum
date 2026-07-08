package tokenutil

// ModelPricing 单模型定价，单位：对应货币/百万 token。
type ModelPricing struct {
	InputPerMillion  float64
	OutputPerMillion float64
	Currency         string // "CNY" | "USD"
}

// staticPricing 与 llmgateway/infrastructure/static_catalog.go 的模型列表一一对应。
// 来源：阿里云百炼（千问）、Z.ai 官网（智谱），以人民币/美元计，均为每百万 token 价格。
var staticPricing = map[string]ModelPricing{
	// 通义千问 (Qwen) —— 单位：CNY/1M tokens
	"qwen-max":          {InputPerMillion: 2.4, OutputPerMillion: 9.6, Currency: "CNY"},
	"qwen-max-latest":   {InputPerMillion: 2.4, OutputPerMillion: 9.6, Currency: "CNY"},
	"qwen-plus":         {InputPerMillion: 0.8, OutputPerMillion: 2.0, Currency: "CNY"},
	"qwen-plus-latest":  {InputPerMillion: 0.8, OutputPerMillion: 2.0, Currency: "CNY"},
	"qwen-turbo":        {InputPerMillion: 0.3, OutputPerMillion: 0.6, Currency: "CNY"},
	"qwen-turbo-latest": {InputPerMillion: 0.3, OutputPerMillion: 0.6, Currency: "CNY"},
	"qwen-long":         {InputPerMillion: 0.5, OutputPerMillion: 2.0, Currency: "CNY"},

	// 智谱 AI (Zhipu / Z.ai) —— 单位：USD/1M tokens
	"glm-5.2":        {InputPerMillion: 1.40, OutputPerMillion: 4.40, Currency: "USD"},
	"glm-4.7-flashx": {InputPerMillion: 0.07, OutputPerMillion: 0.40, Currency: "USD"},
	"glm-4.7-flash":  {InputPerMillion: 0, OutputPerMillion: 0, Currency: "USD"},
	"glm-4.5-flash":  {InputPerMillion: 0, OutputPerMillion: 0, Currency: "USD"},
	"glm-4-plus":     {InputPerMillion: 0.1, OutputPerMillion: 0.1, Currency: "CNY"},
	"glm-4":          {InputPerMillion: 0.1, OutputPerMillion: 0.1, Currency: "CNY"},
	"glm-4-air":      {InputPerMillion: 0.001, OutputPerMillion: 0.001, Currency: "CNY"},
	"glm-4-flash":    {InputPerMillion: 0, OutputPerMillion: 0, Currency: "CNY"},
	"glm-4v":         {InputPerMillion: 0.05, OutputPerMillion: 0.05, Currency: "CNY"},
}

// CostUSD 计算一次调用的成本（统一转换为 USD）。
// CNY 按 7.2 汇率换算，不存在定价的模型返回 0。
func CostUSD(prompt, completion int, model string) float64 {
	p, ok := Lookup(model)
	if !ok {
		return 0
	}
	cost := float64(prompt)/1e6*p.InputPerMillion +
		float64(completion)/1e6*p.OutputPerMillion
	if p.Currency == "CNY" {
		cost /= 7.2
	}
	return cost
}

// Lookup 查询模型定价，支持前缀匹配（"qwen-max-20250101" 命中 "qwen-max"）。
func Lookup(model string) (ModelPricing, bool) {
	if p, ok := staticPricing[model]; ok {
		return p, true
	}
	// 前缀匹配
	for key, p := range staticPricing {
		if len(model) > len(key) && model[:len(key)] == key && model[len(key)] == '-' {
			return p, true
		}
	}
	return ModelPricing{}, false
}
