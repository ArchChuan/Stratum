package application

import "strings"

// sensitiveKeySuffixes 命中即视为凭据字段（全小写包含匹配）。
// 启发式残余风险：标量内嵌 secret（{"command": "curl -H 'Authorization: ...'"}）不命中，文档化接受。
var sensitiveKeySuffixes = []string{
	"token", "key", "password", "secret", "credential", "authorization",
	"passwd", "auth", "session", "bearer", "cookie",
	// review minor：常见凭据键名补充。裸 "sig" 会误伤 design/museum 等词，不收录（"signature" 已覆盖）。
	"pwd", "jwt", "signature", "otp", "passphrase",
}

// RedactSensitivePayload 递归脱敏审批参数中的凭据字段（工作台详情下发前必须调用）。
// 敏感键无论值类型整体掩蔽为 "***"（容器值不递归——防止 {"token": {"value": "sk-1"}}
// 这类嵌套绕过）；非敏感键下的 map/数组递归处理，数组内 map 同样脱敏。
func RedactSensitivePayload(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if isSensitiveKey(k) {
			out[k] = "***"
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

// redactValue 只递归 map[string]any/[]any：JSON 解码路径保证此形态（typed container
// 不可达，潜在形态不做反射兜底——review minor 接受）。
func redactValue(v any) any {
	switch value := v.(type) {
	case map[string]any:
		return RedactSensitivePayload(value)
	case []any:
		items := make([]any, 0, len(value))
		for _, item := range value {
			items = append(items, redactValue(item))
		}
		return items
	default:
		return v
	}
}

func isSensitiveKey(k string) bool {
	lower := strings.ToLower(k)
	for _, suffix := range sensitiveKeySuffixes {
		if strings.Contains(lower, suffix) {
			return true
		}
	}
	return false
}
