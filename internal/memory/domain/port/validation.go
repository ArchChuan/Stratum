package port

import "fmt"

// ValidationError 描述结构化输出中单个字段的校验失败。
// Error() 包含具体值（用于 correction 反馈给模型自修复）；
// Summary() 是不含值的白名单版（用于降级日志，防 PII/原文泄露）。
type ValidationError struct {
	Location string // 出错对象，如 "fact[2]"、"enrichment"、"judgment"
	Field    string // 字段名，如 "importance"、"fact_type"、"content"
	Value    string // 违规的具体值（可能含敏感内容，仅 Error() 使用）
	Reason   string // 失败原因（不含值的静态文案）
}

// Error 包含违规值，供 correction 消息把错误位置/原因/值告诉模型。
func (e *ValidationError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("%s.%s: %s (got %q)", e.Location, e.Field, e.Reason, e.Value)
	}
	return fmt.Sprintf("%s.%s: %s", e.Location, e.Field, e.Reason)
}

// Summary 返回不含具体值的白名单摘要，供降级日志与 typed error 使用。
func (e *ValidationError) Summary() string {
	return fmt.Sprintf("%s.%s: %s", e.Location, e.Field, e.Reason)
}
