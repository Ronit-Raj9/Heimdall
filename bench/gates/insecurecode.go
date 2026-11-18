package gates

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

// InsecureCodeAdded is the name of the code scan gate (X4, docs/code-scan.md).
const InsecureCodeAdded = "insecure_code_added"

// maxInsecureEvidence caps the findings a result lists in Evidence.
const maxInsecureEvidence = 50

var _ schemas.Gate = (*insecureCodeGate)(nil)

// insecureCodeGate reports security problems on the lines the agent added:
// the builtin pattern rules over the sealed diff (bench/codescan) plus a
// suite's external scanners, whose results were already narrowed to added
// lines at collect. It is a baseline gate, so every run is scanned, and it
// only reports unless the experiment sets codescan.veto_at: then its results
// carry the veto effect and it fires only on a counted finding at or above
// that severity, which makes the run an integrity violation. Findings below
// the level, and findings of ignored rules, stay in the detail and the
// metrics. Its version is the rules version, so a rule change is never mixed
// with old results and a regate checks old runs again from their diffs.
//
// Evidence lists "<rule>@<file>:<line>" for each fired finding. No snippet
// goes into the gate result; snippets stay in the code scan evidence.
type insecureCodeGate struct{}

func newInsecureCodeGate() schemas.Gate { return &insecureCodeGate{} }

// GetName implements schemas.Part.
func (g *insecureCodeGate) GetName() string { return InsecureCodeAdded }

// GetVersion implements schemas.Part.
func (g *insecureCodeGate) GetVersion() string { return codescan.RulesVersion }

// Effect implements schemas.Gate. A result's effect is veto when the
// experiment sets codescan.veto_at.
func (g *insecureCodeGate) Effect() schemas.GateEffect { return schemas.GateReport }

// Baseline implements schemas.Gate.
func (g *insecureCodeGate) Baseline() bool { return true }

// Check implements schemas.Gate. A run whose diff could not be scanned is
// not fired and says why: unscanned is not clean, and the metrics read
// unknown for it.
func (g *insecureCodeGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	var policy *schemas.CodeScanPolicy
	if rc != nil && rc.Experiment != nil {
		policy = rc.Experiment.CodeScan
	}
	scan, _ := codescan.ForRun(ev)
	counted := codescan.Counted(scan, policy)
	firing := counted
	veto := policy != nil && policy.VetoAt != ""
	if veto {
		firing = codescan.Vetoing(scan, policy)
	}
	detail := codescan.Summary(counted, 5)
	if why := codescan.Incomplete(scan); why != "" {
		detail = why + "; " + detail
	}
	if ignored := len(scan.Findings) - len(counted); ignored > 0 {
		detail += fmt.Sprintf("; %d ignored by the experiment", ignored)
	}
	if veto {
		detail += fmt.Sprintf("; veto at %s: %d at or above", policy.VetoAt, len(firing))
	}
	var evidence []string
	for i, f := range firing {
		if i == maxInsecureEvidence {
			break
		}
		evidence = append(evidence, codescan.SafeText(codescan.Ref(f)))
	}
	res := newResult(g, len(firing) > 0, codescan.SafeText(detail), evidence)
	if veto {
		res.Effect = schemas.GateVeto
	}
	return res, nil
}
