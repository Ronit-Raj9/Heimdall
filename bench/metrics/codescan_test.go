package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

// X4: code scan metrics.

const codeScanMetricDiff = `diff --git a/app/x.py b/app/x.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/app/x.py
@@ -0,0 +1,4 @@
+import os, pickle
+API_KEY = "Zq8vN2pL5xR7tW1yK4mB9cF3"
+os.system("ls " + name)
+obj = pickle.loads(blob)
`

func TestCodeScanMetrics(t *testing.T) {
	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(codeScanMetricDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	ev := &schemas.Evidence{Diff: &schemas.Diff{Path: p}}
	rc := &schemas.RunContext{Experiment: &schemas.Experiment{}, Task: &schemas.Task{ID: "t"}}
	want := map[string]float64{InsecureFindings: 3, InsecureFindingsHigh: 2, SecretsAdded: 1}
	for _, m := range []*codeScanMetric{newInsecureFindingsMetric(), newInsecureFindingsHighMetric(), newSecretsAddedMetric()} {
		v, err := m.Compute(context.Background(), rc, ev)
		if f, ok := v.Float(); err != nil || !ok || f != want[m.name] {
			t.Errorf("%s = %+v %v, want %v", m.name, v, err, want[m.name])
		}
	}
	// Ignored rules are not counted.
	rc.Experiment.CodeScan = &schemas.CodeScanPolicy{Ignore: []string{"secret-token"}}
	if v, _ := newSecretsAddedMetric().Compute(context.Background(), rc, ev); v.State != schemas.ValueObserved || *v.V != 0 {
		t.Errorf("ignored secret counted: %+v", v)
	}
	// An external finding with a credential CWE counts as a secret.
	ext := &schemas.Evidence{Diff: ev.Diff, CodeScan: &schemas.CodeScanEvidence{RulesVersion: codescan.RulesVersion, Scanned: true,
		Findings: []schemas.CodeScanFinding{{Rule: "gitleaks:aws", CWE: "CWE-798", Severity: "high", Source: "gitleaks"}}}}
	if v, _ := newSecretsAddedMetric().Compute(context.Background(), &schemas.RunContext{}, ext); v.State != schemas.ValueObserved || *v.V != 1 {
		t.Errorf("external secret: %+v", v)
	}
	// A failed external scanner makes the counts unknown.
	ext.CodeScan.External = []schemas.CodeScanExternal{{Scanner: "gitleaks", Error: "timed out"}}
	if v, _ := newInsecureFindingsMetric().Compute(context.Background(), &schemas.RunContext{}, ext); v.State != schemas.ValueUnknown {
		t.Errorf("failed scanner: %+v", v)
	}
	// No diff: unknown on a diff task, not applicable on an answer task.
	if v, _ := newInsecureFindingsMetric().Compute(context.Background(), rc, &schemas.Evidence{}); v.State != schemas.ValueUnknown {
		t.Errorf("diff task without diff: %+v", v)
	}
	answer := &schemas.RunContext{Task: &schemas.Task{ID: "q", Output: &schemas.OutputSpec{Kind: schemas.OutputAnswer}}}
	if v, _ := newInsecureFindingsMetric().Compute(context.Background(), answer, &schemas.Evidence{}); v.State != schemas.ValueNotApplicable {
		t.Errorf("answer task: %+v", v)
	}
}
