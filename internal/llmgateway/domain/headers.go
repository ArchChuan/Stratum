package domain

import (
	"fmt"
	"net/textproto"
	"strings"
)

// blockedHeaderKeys 是 extra_headers 写时固定黑名单：鉴权/传输/客户端 IP
// 伪造头一律拒收。比较前必须经 CanonicalizeHeaderKey 归一化（大小写变体/
// 尾空格穿透是真实覆盖风险）。
var blockedHeaderKeys = map[string]struct{}{
	"Authorization":       {},
	"Content-Type":        {},
	"User-Agent":          {},
	"X-Api-Key":           {},
	"Host":                {},
	"Cookie":              {},
	"Proxy-Authorization": {},
	"Referer":             {},
	"Transfer-Encoding":   {},
	"Content-Length":      {},
	"Trailer":             {},
	"Accept-Encoding":     {},
	"X-Forwarded-For":     {},
	"Forwarded":           {},
}

// CanonicalizeHeaderKey 归一化头名：TrimSpace + MIME 规范形式
// （x-api-key → X-Api-Key，authorization → Authorization）。
func CanonicalizeHeaderKey(k string) string {
	return textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))
}

// ValidateExtraHeaders 校验 provider extra_headers 写入门禁：空 key/黑名单
// 头（含大小写变体与尾空格）/值含控制字符一律拒绝。
func ValidateExtraHeaders(h map[string]string) error {
	for k, v := range h {
		canonical := CanonicalizeHeaderKey(k)
		if canonical == "" {
			return fmt.Errorf("extra_headers: empty header key")
		}
		if _, blocked := blockedHeaderKeys[canonical]; blocked {
			return fmt.Errorf("extra_headers: header %q is blocked", canonical)
		}
		if strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return fmt.Errorf("extra_headers: header %q value contains control characters", canonical)
		}
	}
	return nil
}
