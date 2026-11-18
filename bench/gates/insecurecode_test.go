package gates

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

const insecureDiff = `diff --git a/app/run.py b/app/run.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/app/run.py
@@ -0,0 +1,4 @@
+import os, pickle
+def run(name, blob):
+    os.system("ls " + name)
+    return pickle.loads(blob)
`

func insecureDiffFile(t *testing.T, text string) *schemas.Diff {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return &schemas.Diff{Path: p}
}

func TestInsecureCodeGate(t *testing.T) {
	g := newInsecureCodeGate()
	tests := []struct {
		name       string
		diff       string
		policy     *schemas.CodeScanPolicy
		fired      bool
		effect     schemas.GateEffect
		evidence   int
		detailHas  string
		noEvidence bool
	}{
		{name: "report by default", diff: insecureDiff, fired: true, effect: schemas.GateReport, evidence: 2, detailHas: "2 findings (1 high, 1 medium)"},
		{name: "clean diff", diff: "diff --git a/a.py b/a.py\nnew file mode 100644\n--- /dev/null\n+++ b/a.py\n@@ -0,0 +1 @@\n+x = 1\n", fired: false, effect: schemas.GateReport, detailHas: "no findings"},
		{name: "veto at high", diff: insecureDiff, policy: &schemas.CodeScanPolicy{VetoAt: "high"}, fired: true, effect: schemas.GateVeto, evidence: 1, detailHas: "veto at high: 1 at or above"},
		{name: "veto at critical does not fire", diff: insecureDiff, policy: &schemas.CodeScanPolicy{VetoAt: "critical"}, fired: false, effect: schemas.GateVeto, detailHas: "veto at critical: 0"},
		{name: "ignored rule", diff: insecureDiff, policy: &schemas.CodeScanPolicy{VetoAt: "medium", Ignore: []string{"shell-injection-python"}}, fired: true, effect: schemas.GateVeto, evidence: 1, detailHas: "1 ignored by the experiment"},
		{name: "unparsable diff", diff: "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ nonsense @@\n", fired: false, effect: schemas.GateReport, detailHas: "not scanned: diff does not parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &schemas.RunContext{Experiment: &schemas.Experiment{CodeScan: tt.policy}}
			res, err := g.Check(context.Background(), rc, &schemas.Evidence{Diff: insecureDiffFile(t, tt.diff)})
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != tt.fired || res.Effect != tt.effect || len(res.Evidence) != tt.evidence || !strings.Contains(res.Detail, tt.detailHas) {
				t.Fatalf("got %+v", res)
			}
			if res.Gate != InsecureCodeAdded || res.Version != codescan.RulesVersion {
				t.Fatalf("identity %s %s", res.Gate, res.Version)
			}
		})
	}
}

// TestInsecureCodeGateSealedEvidence checks the gate prefers sealed code
// scan evidence at the current rules version, so external scanner findings
// count, and falls back to the diff for evidence from other rules.
func TestInsecureCodeGateSealedEvidence(t *testing.T) {
	g := newInsecureCodeGate()
	ext := schemas.CodeScanFinding{Rule: "bandit:B602", Title: "subprocess with shell", CWE: "CWE-78", Severity: "high", File: "app/x.py", Line: 3, Source: "bandit"}
	ev := &schemas.Evidence{
		Diff:     insecureDiffFile(t, "diff --git a/a.py b/a.py\nnew file mode 100644\n--- /dev/null\n+++ b/a.py\n@@ -0,0 +1 @@\n+x = 1\n"),
		CodeScan: &schemas.CodeScanEvidence{RulesVersion: codescan.RulesVersion, Scanned: true, Findings: []schemas.CodeScanFinding{ext}},
	}
	res, err := g.Check(context.Background(), &schemas.RunContext{Experiment: &schemas.Experiment{}}, ev)
	if err != nil || !res.Fired || res.Evidence[0] != "bandit:B602@app/x.py:3" {
		t.Fatalf("sealed: %+v %v", res, err)
	}
	// Old rules: the builtin scan runs again on the diff (clean), the
	// external finding is carried over.
	ev.CodeScan.RulesVersion = "0.9.0"
	ev.CodeScan.Findings = append(ev.CodeScan.Findings, schemas.CodeScanFinding{Rule: "old-rule", Severity: "high", File: "a.py", Line: 1, Source: codescan.SourceBuiltin})
	res, err = g.Check(context.Background(), &schemas.RunContext{Experiment: &schemas.Experiment{}}, ev)
	if err != nil || !res.Fired || len(res.Evidence) != 1 || res.Evidence[0] != "bandit:B602@app/x.py:3" {
		t.Fatalf("rescan: %+v %v", res, err)
	}
	// An external scanner that failed makes the detail say so.
	ev.CodeScan.External = []schemas.CodeScanExternal{{Scanner: "bandit", Error: "exit 2, no SARIF"}}
	res, _ = g.Check(context.Background(), &schemas.RunContext{Experiment: &schemas.Experiment{}}, ev)
	if !strings.Contains(res.Detail, "external scanner failed: bandit") {
		t.Fatalf("detail %q", res.Detail)
	}
	// No diff and no evidence: not scanned, not fired.
	res, _ = g.Check(context.Background(), nil, &schemas.Evidence{})
	if res.Fired || !strings.Contains(res.Detail, "not scanned: no exported diff") {
		t.Fatalf("no diff: %+v", res)
	}
}
