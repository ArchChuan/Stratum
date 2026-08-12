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
// 例外：MCP auth 配置（{"type","token","api_key_header","api_key_value",...}）做外科式
// 脱敏——全对象掩蔽会让审批人盲审凭据新增/更换（评审发现 HIGH），破坏知情同意。
func RedactSensitivePayload(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if k == "auth" {
			out[k] = redactMCPAuth(v)
			continue
		}
		if isSensitiveKey(k) {
			out[k] = "***"
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

// redactMCPAuth 对 MCP auth 配置做外科式脱敏。三档判定：
//  1. 凭据值（token/api_key_value/oauth2_client_secret）——掩蔽；
//  2. 决策元数据白名单（type/api_key_header/oauth2_client_id/oauth2_token_url/
//     oauth2_scopes）——明文保留供知情审批。这些键名恰好命中敏感后缀（key/token），
//     必须显式先于后缀过滤判定，否则元数据被误掩（盲审）；
//  3. 其余键——过 isSensitiveKey 后缀过滤：auth 对象可能夹带 {"password"/"secret"/
//     "apiKey"/"sessionToken"} 等凭据（D3/D4 自由格式 args 路径，评审 HIGH），原样
//     透传会泄露。非 map 形态（异常/标量）整体掩蔽。
func redactMCPAuth(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return "***"
	}
	out := make(map[string]any, len(m))
	for k, val := range m {
		switch k {
		case "token", "api_key_value", "oauth2_client_secret":
			out[k] = "***"
		case "type", "api_key_header", "oauth2_client_id", "oauth2_token_url", "oauth2_scopes":
			out[k] = val
		default:
			if isSensitiveKey(k) {
				out[k] = "***"
			} else {
				// 非敏感键仍须递归：auth 对象可能夹带嵌套凭据（{"headers":{"x-api-key":
				// "sk"}}），原样透传会把嵌套 secret 直接给审批人（评审 MEDIUM）。
				out[k] = redactValue(val)
			}
		}
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
