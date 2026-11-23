// Package modelid holds the one rule for "is the served model the model that
// was asked for". Providers answer a request for an alias with the id of the
// snapshot that served it (OpenAI gpt-5.4-mini as gpt-5.4-mini-2026-03-17,
// Anthropic claude-sonnet-4-5 as claude-sonnet-4-5-20250929), so an exact
// string compare counts the same model as a swap. Every place that compares
// a served model with a requested or pinned one uses SameModel.
package modelid

import (
	"regexp"
	"strings"
)

var (
	// dateSuffix is a dated snapshot: -2026-03-17, -20250929 or @20250929.
	dateSuffix = regexp.MustCompile(`^[-@](\d{4}-\d{2}-\d{2}|\d{8})$`)
	// revisionSuffix is a Gemini stable revision: -001.
	revisionSuffix = regexp.MustCompile(`^-\d{3}$`)
	// shortDateSuffix is a 4-digit snapshot: gpt-4-0613, mistral-large-2411.
	shortDateSuffix = regexp.MustCompile(`^-\d{4}$`)
	// ownSuffix finds a date or revision suffix at the end of an id.
	ownSuffix = regexp.MustCompile(`([-@](\d{4}-\d{2}-\d{2}|\d{8})|-\d{3,4})$`)
	// bedrockVendor and bedrockRevision wrap a Bedrock model id:
	// anthropic.claude-3-5-sonnet-20241022-v2:0.
	bedrockVendor   = regexp.MustCompile(`^(anthropic|meta|mistral|amazon|cohere|ai21)\.`)
	bedrockRevision = regexp.MustCompile(`-v\d+(:\d+)?$`)
)

// SameModel reports whether served is the requested model: equal ids after
// dropping a provider prefix and case, or the provider's dated snapshot or
// stable revision of the requested alias. An empty served id is accepted
// (not every provider echoes it). A requested snapshot served as another
// date or as the bare alias is a different model, and a longer family name
// (gpt-5.4 against gpt-5.4-mini-2026-03-17) never matches.
func SameModel(requested, served string) bool {
	if strings.TrimSpace(served) == "" {
		return true
	}
	r, s := normalise(requested, served)
	if r == "" {
		return false
	}
	if r == s {
		return true
	}
	return snapshotOf(r, s) != ""
}

// Snapshot returns the snapshot suffix when served is a dated snapshot of
// requested ("-2026-03-17", "@20250929"), else "". Equal ids, a revision
// suffix and different models give "".
func Snapshot(requested, served string) string {
	r, s := normalise(requested, served)
	if r == "" || s == "" || r == s {
		return ""
	}
	suffix := snapshotOf(r, s)
	if !dateSuffix.MatchString(suffix) {
		return ""
	}
	return suffix
}

// snapshotOf returns the suffix that makes s a snapshot of r, or "" when s
// is not one. r and s are normalised and differ.
func snapshotOf(r, s string) string {
	if base, ok := strings.CutSuffix(r, "-latest"); ok && base != "" {
		if s == base {
			return "-latest"
		}
		r = base
	}
	if ownSuffix.MatchString(r) {
		return "" // a pinned snapshot only matches itself
	}
	rest, ok := strings.CutPrefix(s, r)
	if !ok || rest == "" {
		return ""
	}
	if dateSuffix.MatchString(rest) || revisionSuffix.MatchString(rest) || shortDateSuffix.MatchString(rest) {
		return rest
	}
	return ""
}

// normalise lower-cases both ids and drops everything up to the last "/".
// A served Bedrock id (vendor prefix, optional revision suffix) is unwrapped
// only when the requested id carries neither; a revision suffix alone is
// never dropped, so deepseek-v3 stays a different model from deepseek.
func normalise(requested, served string) (string, string) {
	r, s := lastSegment(requested), lastSegment(served)
	if !bedrockVendor.MatchString(r) && !bedrockRevision.MatchString(r) && bedrockVendor.MatchString(s) {
		s = bedrockRevision.ReplaceAllString(bedrockVendor.ReplaceAllString(s, ""), "")
	}
	return r, s
}

func lastSegment(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m
}
