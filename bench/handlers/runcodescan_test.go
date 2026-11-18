package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// X4: GET /runs/{id}/codescan.

const codeScanTestDiff = `diff --git a/app/run.py b/app/run.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/app/run.py
@@ -0,0 +1,3 @@
+import os
+def run(name):
+    os.system("ls " + name)
`

func TestRunCodeScan(t *testing.T) {
	d := fullDeps()
	idx := stepsFixture(t)
	idx.manifests[testExp].Experiment.CodeScan = &schemas.CodeScanPolicy{VetoAt: "high"}
	write := func(runID, name, body string) {
		if err := os.MkdirAll(filepath.Join(idx.root, runID), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(idx.root, runID, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum := utils.SHA256Bytes([]byte(codeScanTestDiff))
	write("r_t01_A", FileDiff, codeScanTestDiff)
	write("r_t01_A", FileEvidence, `{"diff":{"path":"workspace.diff","sha256":"`+sum+`","files":[]}}`)
	write("r_t01_B", FileDiff, codeScanTestDiff)
	write("r_t01_B", FileEvidence, `{"diff":{"path":"workspace.diff","sha256":"0000","files":[]}}`)
	d.index = idx
	c := newTestClient(t, d)

	resp := c.do(t, "GET", APIPrefix+"/runs/r_t01_A/codescan", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatal(err)
	}
	checkKeys(t, "CodeScanReport", body, loadKeys(t, "CodeScanReport"), true)
	checkKeys(t, "CodeScanReport.scan", objectsAt(t, body, "scan")[0], loadKeys(t, "CodeScanEvidence"), false)
	for _, f := range objectsAt(t, body, "scan.findings[]") {
		checkKeys(t, "CodeScanFinding", f, loadKeys(t, "CodeScanFinding"), false)
	}
	var rep schemas.CodeScanReport
	if err := json.Unmarshal(resp.Body(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Sealed || !rep.Scan.Scanned || rep.Counted != 1 || !rep.Vetoed || rep.VetoAt != "high" ||
		rep.Scan.Findings[0].Rule != "shell-injection-python" || rep.Scan.Findings[0].PatchLine != 9 {
		t.Fatalf("report %+v %+v", rep, rep.Scan)
	}

	// Sealed code scan evidence at the current rules is served as it is.
	write("r_t01_A", FileEvidence, `{"diff":{"path":"workspace.diff","sha256":"`+sum+`","files":[]},"codescan":{"rules_version":"1.0.0","scanned":true,"files":1,"added_lines":3,"findings":[]}}`)
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t01_A/codescan", nil, nil)
	rep = schemas.CodeScanReport{}
	if err := json.Unmarshal(resp.Body(), &rep); err != nil || !rep.Sealed || rep.Counted != 0 || rep.Vetoed {
		t.Fatalf("sealed: %+v %v", rep, err)
	}

	// A diff that does not match its recorded hash is refused.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t01_B/codescan", nil, nil)
	if resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "does not match") {
		t.Fatalf("mismatch: %d %s", resp.StatusCode(), resp.Body())
	}

	// A run with no diff: scanned false with the reason.
	resp = c.do(t, "GET", APIPrefix+"/runs/r_t02_A/codescan", nil, nil)
	rep = schemas.CodeScanReport{}
	if err := json.Unmarshal(resp.Body(), &rep); err != nil || resp.StatusCode() != 200 || rep.Scan.Scanned || rep.Scan.Reason != "no exported diff" {
		t.Fatalf("no diff: %d %+v", resp.StatusCode(), rep)
	}

	// An unknown run is 404.
	if resp := c.do(t, "GET", APIPrefix+"/runs/r_nope/codescan", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("unknown run: %d", resp.StatusCode())
	}
}
