package domain

import (
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var sensitiveText = regexp.MustCompile(
	`(?i)\b(password|token|api_key|apikey|authorization|secret)=((bearer|basic)\s+)?\S+`,
)

// SanitizeValue 递归脱敏任意结构化值（落库 / 入评审池快照前调用，spec §6.5）：
// 敏感 key（password/token/api_key/apikey/authorization/secret 等）整体替换为
// [REDACTED]，字符串中的敏感键值对用 sensitiveText 正则替换，普通字段原样保留。
// 返回新结构，不修改入参。
func SanitizeValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if isSensitiveKey(key) {
				out[key] = redacted
				continue
			}
			out[key] = SanitizeValue(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = SanitizeValue(item)
		}
		return out
	case string:
		return sensitiveText.ReplaceAllString(v, "$1="+redacted)
	default:
		return value
	}
}

// isSensitiveKey 判定 JSON key 是否属于敏感凭据字段：命中则整体脱敏为 [REDACTED]。
// key 先归一化（连字符转下划线与小写），覆盖 snake_case 与 kebab-case 变体。
func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "password", "token", "api_key", "apikey", "authorization", "secret", "access_token", "refresh_token":
		return true
	default:
		return false
	}
}

// SanitizeTools 落库 / 入评审池快照前脱敏工具序列（spec §6.5）：Arguments 复用
// SanitizeValue 递归脱敏（敏感 key 与内嵌键值），RawText 用 sensitiveText 正则替换
// 敏感键值对；其余字段原样透传。返回新切片，不修改入参。
func SanitizeTools(tools []ToolObservation) []ToolObservation {
	if len(tools) == 0 {
		return tools
	}
	out := make([]ToolObservation, len(tools))
	for i, tool := range tools {
		out[i] = tool
		if tool.Arguments != nil {
			if args, ok := SanitizeValue(tool.Arguments).(map[string]any); ok {
				out[i].Arguments = args
			}
		}
		out[i].RawText = sensitiveText.ReplaceAllString(tool.RawText, "$1="+redacted)
	}
	return out
}
