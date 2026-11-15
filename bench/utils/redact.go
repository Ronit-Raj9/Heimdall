package utils

import "regexp"

var secretPattern = regexp.MustCompile(`(?i)(authorization|api[_-]?key|access[_-]?token|token|secret|password)\s*[:=]\s*([^\s,;]+)`)
var bearerPattern = regexp.MustCompile(`(?i)authorization\s*[:=]\s*(?:bearer\s+)?[^\s,;]+`)
var jsonSecretPattern = regexp.MustCompile(`(?i)"(authorization|api[_-]?key|access[_-]?token|token|secret|password)"\s*:\s*"[^"]*"`)

// RedactSecrets removes common key-value secret forms before evidence is
// persisted. It does not alter the source value used for in-memory decisions.
func RedactSecrets(s string) string {
	s = bearerPattern.ReplaceAllString(s, "Authorization=[redacted]")
	s = secretPattern.ReplaceAllString(s, "$1=[redacted]")
	return jsonSecretPattern.ReplaceAllString(s, `"$1":"[redacted]"`)
}

// HasUnredactedSecrets reports whether the text contains a secret-shaped
// key-value or authorization field that RedactSecrets would change.
func HasUnredactedSecrets(s string) bool {
	return RedactSecrets(s) != s
}
