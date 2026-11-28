package handlers

import (
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// stepKeyPattern matches key-shaped strings in agent-influenced step text:
// the per-run Bifrost virtual key (sk-bf-...), even only its start, other
// sk- keys, bearer tokens and common token prefixes. A step summary is often
// a shell command the agent ran, cut to a fixed length, so a key may be cut
// short; any 4 or more key characters after a known prefix are masked.
var stepKeyPattern = regexp.MustCompile(`(?i)(sk-bf-[A-Za-z0-9_-]{2,}|sk-[A-Za-z0-9_-]{12,}|sk_[A-Za-z0-9]{12,}|gh[pousr]_[A-Za-z0-9]{12,}|xox[abpr]-[A-Za-z0-9-]{8,}|AKIA[0-9A-Z]{12,})|(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)

// maskKeyShaped replaces key-shaped substrings of s with a masked form that
// keeps only the prefix, so a reader sees a key was there.
func maskKeyShaped(s string) string {
	if s == "" {
		return s
	}
	return stepKeyPattern.ReplaceAllStringFunc(s, func(m string) string {
		sub := stepKeyPattern.FindStringSubmatch(m)
		if sub[2] != "" {
			return sub[2] + "[masked]"
		}
		switch {
		case len(m) >= 6 && (m[:6] == "sk-bf-" || m[:6] == "SK-BF-"):
			return m[:6] + "[masked]"
		default:
			return "[masked]"
		}
	})
}

// maskStepSecrets masks key-shaped strings in the display text of rows.
func maskStepSecrets(steps []schemas.StepRecord) {
	for i := range steps {
		steps[i].Summary = maskKeyShaped(steps[i].Summary)
		steps[i].Path = maskKeyShaped(steps[i].Path)
		steps[i].ToolName = maskKeyShaped(steps[i].ToolName)
	}
}
