package sharelinks

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// HiddenPath replaces a server or worker file path inside a shared string.
const HiddenPath = "[hidden path]"

// platformIDRe matches the platform's own ids of orgs, people, invitations,
// API tokens and runners (db.NewID: prefix, "_", 26 Crockford base32
// digits). Error texts and reasons can carry them; a shared page never does.
// No word boundaries: an id is often one part of a longer name joined with
// "_" or "-" (a gateway key slot is "<org id>_<slot>"), and "_" counts as a
// word character.
var platformIDRe = regexp.MustCompile(`(?:org|usr|inv|tok|rnr)_[0-9a-hjkmnp-tv-z]{26}`)

// HideServerText returns v (decoded JSON, as Redact returns it) with every
// string scrubbed of the server's own folders and of platform ids: a path
// that starts with one of roots becomes HiddenPath up to the next space or
// quote, and an org, user, invitation, token or runner id becomes Hidden.
// Error texts in a shared answer (a release rule set that could not be
// read, say) otherwise name the folder of the organization on the server
// (fix wave 3, N293 review). Roots that are empty or not absolute are
// ignored.
func HideServerText(v any, roots ...string) any {
	clean := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" || !filepath.IsAbs(r) {
			continue
		}
		r = filepath.Clean(r)
		if r == "/" {
			continue
		}
		clean = append(clean, r)
	}
	// Longest first, so a nested root is not cut by its parent's match.
	sort.Slice(clean, func(i, j int) bool { return len(clean[i]) > len(clean[j]) })
	return hideServerText(v, clean)
}

func hideServerText(v any, roots []string) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = hideServerText(val, roots)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = hideServerText(val, roots)
		}
		return out
	case string:
		return hideInString(t, roots)
	}
	return v
}

func hideInString(s string, roots []string) string {
	for _, r := range roots {
		from := 0
		for {
			i := strings.Index(s[from:], r)
			if i < 0 {
				break
			}
			i += from
			end := i + len(r)
			if end < len(s) && s[end] != '/' && !pathEnd(s[end]) {
				// A longer name that only starts like the root
				// (/data/runs2 against /data/runs): leave it.
				from = end
				continue
			}
			for end < len(s) && !pathEnd(s[end]) {
				end++
			}
			s = s[:i] + HiddenPath + s[end:]
			from = i + len(HiddenPath)
		}
	}
	return platformIDRe.ReplaceAllString(s, Hidden)
}

// pathEnd reports whether c ends a path inside running text.
func pathEnd(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', ',', ';', ':', ')', ']', '}', '>':
		return true
	}
	return false
}

// HideServerString is HideServerText for one text (an exported report
// document).
func HideServerString(s string, roots ...string) string {
	if v, ok := HideServerText(s, roots...).(string); ok {
		return v
	}
	return s
}
