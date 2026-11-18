package codescan

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ForRun returns a run's code scan. Sealed code scan evidence made by the
// current rules is used as it is; otherwise (a run sealed before the code
// scan existed, or under other rules) the builtin rules run again on the
// run's sealed diff at ev.Diff.Path, and the sealed external scanner results
// are carried over, because those cannot be run again from evidence. sealed
// reports which of the two it is. A run with no diff evidence is not
// scanned.
func ForRun(ev *schemas.Evidence) (scan *schemas.CodeScanEvidence, sealed bool) {
	return forRun(ev, func() *schemas.CodeScanEvidence {
		if ev.Diff == nil || ev.Diff.Path == "" {
			return notScanned("no exported diff")
		}
		return ScanFile(ev.Diff.Path)
	})
}

// ForDiff is ForRun with the diff's bytes in hand (nil when the run has no
// diff), for readers that get run files as bytes.
func ForDiff(ev *schemas.Evidence, diff []byte) (scan *schemas.CodeScanEvidence, sealed bool) {
	return forRun(ev, func() *schemas.CodeScanEvidence {
		if diff == nil {
			return notScanned("no exported diff")
		}
		return Scan(diff)
	})
}

func forRun(ev *schemas.Evidence, rescan func() *schemas.CodeScanEvidence) (*schemas.CodeScanEvidence, bool) {
	if ev == nil {
		ev = &schemas.Evidence{}
	}
	if ev.CodeScan != nil && ev.CodeScan.RulesVersion == RulesVersion {
		return ev.CodeScan, true
	}
	out := rescan()
	if ev.Diff != nil {
		out.DiffSHA256 = ev.Diff.SHA256
	}
	if old := ev.CodeScan; old != nil {
		for _, f := range old.Findings {
			if f.Source != SourceBuiltin {
				out.Findings = append(out.Findings, f)
			}
		}
		out.External = old.External
		sortFindings(out.Findings)
	}
	return out, false
}

// Counted returns the findings that count under the policy: every finding
// whose rule the policy does not ignore. Ignore names a builtin rule id or
// "<scanner>:<rule>" for an external scanner's rule.
func Counted(scan *schemas.CodeScanEvidence, p *schemas.CodeScanPolicy) []schemas.CodeScanFinding {
	if scan == nil {
		return nil
	}
	var out []schemas.CodeScanFinding
	for _, f := range scan.Findings {
		if p != nil && slices.Contains(p.Ignore, f.Rule) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Vetoing returns the counted findings at or above the policy's veto level,
// or nil when the policy sets none.
func Vetoing(scan *schemas.CodeScanEvidence, p *schemas.CodeScanPolicy) []schemas.CodeScanFinding {
	if p == nil || p.VetoAt == "" {
		return nil
	}
	floor := schemas.CodeScanSeverityRank(p.VetoAt)
	var out []schemas.CodeScanFinding
	for _, f := range Counted(scan, p) {
		if schemas.CodeScanSeverityRank(f.Severity) >= floor {
			out = append(out, f)
		}
	}
	return out
}

// Incomplete returns why the scan's counts are not complete: the diff was
// not scanned, or an external scanner the suite declared failed. "" when the
// counts are whole.
func Incomplete(scan *schemas.CodeScanEvidence) string {
	if scan == nil {
		return "no code scan"
	}
	if !scan.Scanned {
		return "not scanned: " + scan.Reason
	}
	var failed []string
	for _, x := range scan.External {
		if x.Error != "" {
			failed = append(failed, x.Scanner+": "+x.Error)
		}
	}
	if len(failed) > 0 {
		return "external scanner failed: " + strings.Join(failed, "; ")
	}
	return ""
}

// CheckPolicy validates an experiment's code scan setting: a known veto
// level and ignored rules that are builtin ids or "<scanner>:<rule>". The
// planner refuses an experiment that fails it.
func CheckPolicy(p *schemas.CodeScanPolicy) error {
	if p == nil {
		return nil
	}
	if p.VetoAt != "" && schemas.CodeScanSeverityRank(p.VetoAt) == 0 {
		return fmt.Errorf("codescan.veto_at %q is not one of %s", p.VetoAt, strings.Join(schemas.CodeScanSeverities, ", "))
	}
	for _, id := range p.Ignore {
		if _, ok := RuleByID(id); ok {
			continue
		}
		if scanner, rule, ok := strings.Cut(id, ":"); ok && scanner != "" && rule != "" {
			continue
		}
		return fmt.Errorf("codescan.ignore: %q is neither a builtin rule (bifrost-bench codescan rules) nor <scanner>:<rule>", id)
	}
	return nil
}

// Summary describes the counted findings in one line: the count by
// severity and the first few as rule@file:line.
func Summary(fs []schemas.CodeScanFinding, max int) string {
	if len(fs) == 0 {
		return "no findings"
	}
	bySev := map[string]int{}
	for _, f := range fs {
		bySev[f.Severity]++
	}
	var sev []string
	for i := len(schemas.CodeScanSeverities) - 1; i >= 0; i-- {
		if n := bySev[schemas.CodeScanSeverities[i]]; n > 0 {
			sev = append(sev, fmt.Sprintf("%d %s", n, schemas.CodeScanSeverities[i]))
		}
	}
	var refs []string
	for i, f := range fs {
		if i == max {
			refs = append(refs, fmt.Sprintf("and %d more", len(fs)-max))
			break
		}
		refs = append(refs, Ref(f))
	}
	noun := "findings"
	if len(fs) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("%d %s (%s): %s", len(fs), noun, strings.Join(sev, ", "), strings.Join(refs, ", "))
}

// Ref names a finding as rule@file:line, with "(removed)" for a removed line.
func Ref(f schemas.CodeScanFinding) string {
	s := fmt.Sprintf("%s@%s:%d", f.Rule, f.File, f.Line)
	if f.Removed {
		s += " (removed)"
	}
	return s
}

// BuildReport is a run's code scan as the API and `codescan diff` show it:
// the findings with where they came from (ForRun or ForDiff), and what the
// experiment's policy makes of them.
func BuildReport(runID string, scan *schemas.CodeScanEvidence, sealed bool, p *schemas.CodeScanPolicy) *schemas.CodeScanReport {
	out := &schemas.CodeScanReport{RunID: runID, Sealed: sealed, Scan: scan, Counted: len(Counted(scan, p))}
	if p != nil {
		out.VetoAt, out.Ignored = p.VetoAt, p.Ignore
		out.Vetoed = len(Vetoing(scan, p)) > 0
	}
	return out
}
