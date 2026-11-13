package main

import (
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// short returns the first 12 characters of a hash for display.
func short(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// trimExt drops a file extension.
func trimExt(p string) string {
	return strings.TrimSuffix(p, filepath.Ext(p))
}

// parseDecisions turns "regressed,invalid" into decisions.
func parseDecisions(s string) []schemas.Decision {
	var out []schemas.Decision
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, schemas.Decision(part))
		}
	}
	return out
}
