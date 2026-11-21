package suite

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Lint waivers (schemas.LintWaiver). An imported library task keeps its
// upstream text verbatim; two static checks are heuristics that such text
// can trip without leaking anything: the instruction-words rule (an upstream
// instruction that says "hidden tests" or "grader" in passing) and the
// high-entropy *_KEY/*_TOKEN value pattern (identifiers such as irange_key
// in upstream source). A waiver names the rule and the exact match, and
// turns that one error into a warning that still shows. Nothing else can be
// waived: hidden tests visible in the repo, real secret patterns (API keys,
// tokens), sizes and every other check stay errors.

// InstructionLeakTerms are the leak words the instruction rule finds in text.
func InstructionLeakTerms(text string) []string {
	lower := strings.ToLower(text)
	var out []string
	for _, term := range leakTerms {
		if strings.Contains(lower, term) {
			out = append(out, term)
		}
	}
	return out
}

// ApplyLintWaivers turns the errors of ps that a waiver of t covers into
// warnings. It is pure, and does nothing for a task without
// provenance.imported_from.
func ApplyLintWaivers(root string, t *schemas.Task, ps []Problem) []Problem {
	if t == nil || len(t.LintWaivers) == 0 || t.Provenance == nil || t.Provenance.ImportedFrom == "" {
		return ps
	}
	instr := rel(root, filepath.Join(t.Dir, filepath.FromSlash(t.Instruction)))
	out := make([]Problem, len(ps))
	copy(out, ps)
	for i, p := range out {
		if p.Severity != SeverityError {
			continue
		}
		for _, w := range t.LintWaivers {
			if waives(root, t, instr, w, p) {
				out[i].Severity = SeverityWarning
				out[i].Message = fmt.Sprintf("waived (%s: %s): %s", w.Rule, w.Reason, p.Message)
				break
			}
		}
	}
	return out
}

func waives(root string, t *schemas.Task, instr string, w schemas.LintWaiver, p Problem) bool {
	switch w.Rule {
	case schemas.LintRuleInstructionTerm:
		return p.Path == instr && strings.HasPrefix(p.Message, fmt.Sprintf("mentions %q;", w.Match))
	case schemas.LintRuleHighEntropy:
		if w.Path == "" || w.Line <= 0 || !strings.HasSuffix(w.Match, "=****") {
			return false
		}
		want := rel(root, filepath.Join(t.Dir, filepath.FromSlash(w.Path)))
		return p.Path == want && strings.HasPrefix(p.Message, fmt.Sprintf("line %d: possible high-entropy ", w.Line)) &&
			strings.Contains(p.Message, "("+w.Match+")")
	}
	return false
}
