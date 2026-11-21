package auth

import (
	"slices"
	"strings"
)

// API token scopes, from narrowest to widest. A scope narrows what a token
// may do below its role; it never widens it (tenancy.CheckScope enforces
// them per route, docs/api-v1.md lists which routes need which).
const (
	ScopeRead     = "read"
	ScopeEvaluate = "evaluate"
	ScopeAdmin    = "admin"
)

// Scopes lists every valid scope, narrowest first.
var Scopes = []string{ScopeRead, ScopeEvaluate, ScopeAdmin}

func scopeRank(s string) int {
	return slices.Index(Scopes, s) + 1
}

// ScopesAllow reports whether a token holding scopes may make a request that
// needs need: admin covers evaluate and read, evaluate covers read.
func ScopesAllow(scopes []string, need string) bool {
	want := scopeRank(need)
	if want == 0 {
		return false
	}
	for _, s := range scopes {
		if scopeRank(s) >= want {
			return true
		}
	}
	return false
}

// NormalizeScopes validates, deduplicates and orders requested scopes. It
// returns ok false for an unknown scope.
func NormalizeScopes(in []string) (out []string, ok bool) {
	seen := map[string]bool{}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if scopeRank(s) == 0 {
			return nil, false
		}
		seen[s] = true
	}
	for _, s := range Scopes {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out, true
}

// JoinScopes is the stored and displayed form: comma-separated.
func JoinScopes(scopes []string) string { return strings.Join(scopes, ",") }

// SplitScopes parses the stored form. An unknown entry is kept as it is:
// it grants nothing, and a token holding only unknown entries stays
// restricted rather than falling back to its role alone.
func SplitScopes(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.ToLower(strings.TrimSpace(v)); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
