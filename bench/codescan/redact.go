package codescan

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Redact returns a line as a finding's snippet: trimmed, with every secret
// the bench's scanners know masked, and at most maxSnippetLen characters. A
// line that holds a token, key or private key the admission scanner
// (suite.ScanSecretsIn) recognizes is replaced whole by its masked forms,
// because that scanner reports what it found but not where; key-value
// secrets go through the seal scanner's redaction (utils.RedactSecrets), so a
// snippet can never stop a run folder from sealing.
func Redact(p, line string) string {
	s := strings.TrimSpace(line)
	if hits := suite.ScanSecretsIn(p, []byte(s)); len(hits) > 0 {
		masked := make([]string, 0, len(hits))
		for _, h := range hits {
			masked = append(masked, h.Masked)
		}
		s = "[line redacted: " + strings.Join(masked, ", ") + "]"
	}
	s = clip(utils.RedactSecrets(s), maxSnippetLen)
	if utils.HasUnredactedSecrets(s) { // the cut left a key-value form the seal scanner would refuse
		s = utils.RedactSecrets(s)
	}
	return sealSafe(s)
}

// redactedEnd matches a redaction marker the seal scanner would read on
// into: its value pattern runs to the next space, comma or semicolon, so
// "key=[redacted]" followed by a quote (a JSON string's end, a closing
// bracket) reads as a value it still has to redact.
var redactedEnd = regexp.MustCompile(`\[redacted\]([^\s,;]|$)`)

// sealSafe ends every redaction marker with a semicolon unless a space,
// comma or semicolon already ends it, so the snippet passes the seal's secret
// scan inside a JSON file too.
func sealSafe(s string) string {
	return redactedEnd.ReplaceAllString(s, "[redacted];$1")
}

// SafeText redacts key-value secrets in text the bench writes about a scan
// (a gate detail, a scanner's error) and keeps the result safe for the
// seal's secret scan.
func SafeText(s string) string {
	return sealSafe(utils.RedactSecrets(s))
}

// clip cuts s to at most n runes, marking the cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-3]) + "..."
}
