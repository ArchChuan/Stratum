// Package safetext provides deterministic redaction for text crossing trust boundaries.
package safetext

import "regexp"

var (
	authorizationCredential = regexp.MustCompile(`(?i)(authorization[ \t]*[:=][ \t]*)(?:(?:bearer|basic)[ \t]+)?(?:"[^"\r\n]*"|'[^'\r\n]*'|[^ \t,;}\]\r\n]+)`)
	namedCredential         = regexp.MustCompile(`(?i)((?:"(?:password|token|api[_-]?key|apikey|secret)"|(?:password|token|api[_-]?key|apikey|secret))[ \t]*[:=][ \t]*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^ \t,;}\]\r\n]+)`)
)

// RedactCredentials replaces credential values without consuming adjacent fields.
func RedactCredentials(value string) string {
	value = authorizationCredential.ReplaceAllString(value, `${1}"[REDACTED]"`)
	return namedCredential.ReplaceAllString(value, `${1}"[REDACTED]"`)
}
