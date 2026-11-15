package clusters

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// maxLineRunes caps a normalized line, so one huge stack frame or JSON blob
// never becomes a multi-kilobyte fingerprint.
const maxLineRunes = 160

// Placeholders the normalizer writes in place of volatile tokens.
const (
	phTime = "<ts>"
	phUUID = "<uuid>"
	phHex  = "<hex>"
	phID   = "<id>"
	phPath = "<path>"
	phNum  = "<n>"
)

// Normalizer patterns, applied in this order. Timestamps and uuids go first
// because their digits and dashes would otherwise be split into numbers.
var (
	reTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}(?:[t ]\d{2}:\d{2}(?::\d{2})?(?:[.,]\d+)?(?:z|[+-]\d{2}:?\d{2})?)?|\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	reUUID      = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reHexPrefix = regexp.MustCompile(`\b0x[0-9a-f]+\b`)
	reHexWord   = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)
	reToken     = regexp.MustCompile(`[a-z0-9_\-]{16,}`)
	rePathToken = regexp.MustCompile(`[\w.\-~@]*(?:/[\w.\-~@]+)+/?(?::\d+(?::\d+)?)?`)
	reLineNo    = regexp.MustCompile(`\blines? \d+\b`)
	reNumber    = regexp.MustCompile(`\b\d+(?:\.\d+)*`)
	reSpaces    = regexp.MustCompile(`\s+`)
	reTraceback = regexp.MustCompile(`^traceback \(most recent call last\)`)
)

// errorWords mark a line that reports a failure. Stderr lines without one are
// banners and progress output ("Reading additional input from stdin...") and
// never become a fingerprint.
var errorWords = []string{
	"error", "exception", "fatal", "panic", "traceback", "failed", "failure", "denied",
	"refused", "cannot", "can't", "unable", "invalid", "not found", "timeout", "timed out",
	"killed", "abort", "unavailable", "overloaded", "rate limit", "segmentation", "no such",
}

// NormalizeLine makes one line of error text stable across runs: it lowers
// the case, then replaces timestamps, uuids, hex strings, long ids and
// tokens (which also hides anything key-shaped), file paths with their
// line numbers, "line N" references and every other number with
// placeholders, collapses whitespace and clips the result. A standalone
// three-digit number from 100 to 599 is kept: it is almost always an HTTP
// status, and "500" versus "404" is the difference a reader needs. It is
// pure.
func NormalizeLine(s string) string {
	s = strings.ToLower(s)
	s = reTimestamp.ReplaceAllString(s, phTime)
	s = reUUID.ReplaceAllString(s, phUUID)
	s = reHexPrefix.ReplaceAllString(s, phHex)
	s = reHexWord.ReplaceAllStringFunc(s, func(w string) string {
		if strings.ContainsAny(w, "0123456789") {
			return phHex
		}
		return w
	})
	s = reToken.ReplaceAllStringFunc(s, func(w string) string {
		if strings.ContainsAny(w, "0123456789") && strings.ContainsAny(w, "abcdefghijklmnopqrstuvwxyz") {
			return phID
		}
		return w
	})
	s = rePathToken.ReplaceAllStringFunc(s, func(w string) string {
		if strings.ContainsAny(w, "abcdefghijklmnopqrstuvwxyz") {
			return phPath
		}
		return w
	})
	s = reLineNo.ReplaceAllString(s, "line "+phNum)
	s = replaceNumbers(s)
	s = strings.TrimSpace(reSpaces.ReplaceAllString(s, " "))
	return clip(s, maxLineRunes)
}

// FirstStableLine returns the first line of text that is still informative
// after NormalizeLine: it has a letter outside the placeholders and is not
// only punctuation. With needErrorWord, a line must also contain one of the
// error words, so banners and progress lines are skipped. A Python
// traceback is the exception: its header line says nothing, so when one
// appears the last stable line (the exception) is returned instead. It
// returns "" when no line qualifies. It is pure.
func FirstStableLine(text string, needErrorWord bool) string {
	var first, last string
	traceback := false
	for _, raw := range strings.Split(text, "\n") {
		line := NormalizeLine(raw)
		if !stable(line) {
			continue
		}
		if reTraceback.MatchString(line) {
			traceback = true
			continue
		}
		if needErrorWord && !hasErrorWord(line) && !traceback {
			continue
		}
		if first == "" {
			first = line
		}
		last = line
	}
	if traceback && last != "" {
		return last
	}
	return first
}

// replaceNumbers replaces every number with phNum, except a standalone
// three-digit number from 100 to 599 (an HTTP status): one followed by a
// letter or digit ("250ms") is a measurement and is replaced.
func replaceNumbers(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range reNumber.FindAllStringIndex(s, -1) {
		w := s[m[0]:m[1]]
		b.WriteString(s[last:m[0]])
		last = m[1]
		n, err := strconv.Atoi(w)
		next := byte(' ')
		if m[1] < len(s) {
			next = s[m[1]]
		}
		alnum := (next >= 'a' && next <= 'z') || (next >= '0' && next <= '9') || next == '_'
		if err == nil && len(w) == 3 && n >= 100 && n <= 599 && !alnum {
			b.WriteString(w)
			continue
		}
		b.WriteString(phNum)
	}
	b.WriteString(s[last:])
	return b.String()
}

// Hash returns a short stable hash of parts: the first 12 hex characters of
// the sha256 of the parts joined by a unit separator.
func Hash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])[:12]
}

// stable reports whether a normalized line still carries words: once the
// placeholders are removed, at least two letters remain.
func stable(line string) bool {
	if line == "" {
		return false
	}
	rest := line
	for _, ph := range []string{phTime, phUUID, phHex, phID, phPath, phNum} {
		rest = strings.ReplaceAll(rest, ph, "")
	}
	letters := 0
	for _, r := range rest {
		if r >= 'a' && r <= 'z' {
			letters++
		}
	}
	return letters >= 2
}

// hasErrorWord reports whether a normalized line contains an error word.
func hasErrorWord(line string) bool {
	for _, w := range errorWords {
		if strings.Contains(line, w) {
			return true
		}
	}
	return false
}

// clip shortens s to n runes with an ellipsis.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
