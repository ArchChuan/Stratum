// Package textutil 提供跨 context 复用的文本处理工具。
package textutil

// TruncateRunes 返回字符串前 maxRunes 个 Unicode rune，按字符截断、不会在
// 多字节字符中间切断；字符串长度不足时原样返回。maxRunes <= 0 时返回空串，
// 避免对负值做 rune slice 造成 panic。
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
