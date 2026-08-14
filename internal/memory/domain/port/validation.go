package port

import "fmt"

// ValidationError 描述结构化输出中单个字段的校验失败。
// Error() 包含具体值（用于 correction 反馈给模型自修复）；
// Summary() 是不含值的白名单版（用于降级日志，防 PII/原文泄露）。
type ValidationError struct {
	Location  string // 出错对象，如 "fact[2]"、"enrichment"、"judgment"
	FieldName string // 字段名，如 "importance"、"fact_type"、"content"
	Value     string // 违规的具体值（可能含敏感内容，仅 Error() 使用）
	Reason    string // 失败原因（不含值的静态文案）
}

// Error 包含违规值，供 correction 消息把错误位置/原因/值告诉模型。
func (e *ValidationError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("%s.%s: %s (got %q)", e.Location, e.FieldName, e.Reason, e.Value)
	}
	return fmt.Sprintf("%s.%s: %s", e.Location, e.FieldName, e.Reason)
}

// Summary 返回不含具体值的白名单摘要，供降级日志与 typed error 使用。
func (e *ValidationError) Summary() string {
	return fmt.Sprintf("%s.%s: %s", e.Location, e.FieldName, e.Reason)
}

// Field 返回字段名，实现 llmdomain.FieldError（结构化失败白名单摘要）。
// 字段名存于 FieldName：Go 禁止字段与方法同名，方法名固定为 Field 以满足
// FieldError 接口。nil-safe：typed-nil（(*ValidationError)(nil) 包进 error
// 接口）经 errors.As 命中类型后由内核调用 Field() 不 panic，返回空串。
func (e *ValidationError) Field() string {
	if e == nil {
		return ""
	}
	return e.FieldName
}
