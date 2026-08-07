// Package safetext provides deterministic redaction for text crossing trust boundaries.
package safetext

import "regexp"

var (
	authorizationCredential = regexp.MustCompile(`(?i)((?:"authorization"|authorization)[ \t]*[:=][ \t]*)(?:(?:bearer|basic)[ \t]+)?(?:"[^"\r\n]*"|'[^'\r\n]*'|[^ \t,;}\]\r\n]+)`)
	// namedCredential matches any key containing a credential token as a
	// substring, so composite names (oauth2_client_secret, X-Api-Key,
	// ANTHROPIC_API_KEY, api_key_value, secret1, token2) are redacted too.
	// The token must be followed by a key-name separator (_, -, .), a digit,
	// a quote, or the key end, to avoid matching ordinary words such as
	// "tokens" or "tokenizer"; the run may then continue with key-name
	// characters. Quoted JSON keys terminate at the closing `"`, so a match
	// can never span multiple fields, and raw keys cannot span lines or
	// swallow values.
	namedCredential = regexp.MustCompile(`(?i)((?:"[^"\r\n]*(?:password|token|api[_-]?key|apikey|secret)(?:[0-9]|[^a-z0-9"][^"\r\n]*)*"|[a-z0-9_.-]*(?:password|token|api[_-]?key|apikey|secret)(?:[0-9]+|[_.-][a-z0-9_.-]*)*)[ \t]*[:=][ \t]*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^ \t,;}\]\r\n]+)`)
)

// RedactCredentials replaces credential values without consuming adjacent fields.
func RedactCredentials(value string) string {
	value = authorizationCredential.ReplaceAllString(value, `${1}"[REDACTED]"`)
	return namedCredential.ReplaceAllString(value, `${1}"[REDACTED]"`)
}
