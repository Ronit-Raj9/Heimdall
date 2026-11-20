package main

import (
	"fmt"
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

// parseDecisions turns "regressed,invalid" into decisions. An unknown
// decision is an error: a misspelled -fail-on would otherwise silently
// never fail the gate (audit 5-B-10).
func parseDecisions(s string) ([]schemas.Decision, error) {
	var out []schemas.Decision
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		switch d := schemas.Decision(part); d {
		case schemas.DecisionImproved, schemas.DecisionRegressed, schemas.DecisionInconclusive, schemas.DecisionInvalid:
			out = append(out, d)
		default:
			return nil, fmt.Errorf("-fail-on: unknown verdict %q (want improved, regressed, inconclusive or invalid)", part)
		}
	}
	return out, nil
}
