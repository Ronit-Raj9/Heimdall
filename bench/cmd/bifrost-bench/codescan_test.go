package main

// X4: `codescan rules` and `codescan diff -file`.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

const cliInsecureDiff = `diff --git a/app/run.py b/app/run.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/app/run.py
@@ -0,0 +1,4 @@
+import os, random
+def run(name):
+    os.system("ls " + name)
+    reset_token = random.random()
`

func TestCodeScanCommand(t *testing.T) {
	code, out := captureOutput(t, func() int { return runCodeScanCmd([]string{"rules", "-json"}) })
	var rules struct {
		RulesVersion string                 `json:"rules_version"`
		Rules        []schemas.CodeScanRule `json:"rules"`
	}
	if code != exitOK || json.Unmarshal([]byte(out), &rules) != nil || rules.RulesVersion != codescan.RulesVersion || len(rules.Rules) != len(codescan.Rules()) {
		t.Fatalf("rules: %d %q", code, out)
	}

	p := filepath.Join(t.TempDir(), "workspace.diff")
	if err := os.WriteFile(p, []byte(cliInsecureDiff), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out = captureOutput(t, func() int { return runCodeScanCmd([]string{"diff", "-file", p}) })
	if code != exitOK || !strings.Contains(out, "2 findings on 4 added lines in 1 files") || !strings.Contains(out, "shell-injection-python") || !strings.Contains(out, "insecure-random") {
		t.Fatalf("diff: %d %q", code, out)
	}
	for flag, want := range map[string]int{"high": exitCodeScanFindings, "critical": exitOK, "medium": exitCodeScanFindings} {
		code, _ = captureOutput(t, func() int { return runCodeScanCmd([]string{"diff", "-file", p, "-fail-at", flag, "-json"}) })
		if code != want {
			t.Errorf("-fail-at %s: exit %d, want %d", flag, code, want)
		}
	}
	for _, args := range [][]string{{}, {"nope"}, {"diff"}, {"diff", "r_x", "-file", p}, {"diff", "-file", p, "-fail-at", "severe"}} {
		if code, _ := captureOutput(t, func() int { return runCodeScanCmd(args) }); code != exitUsage {
			t.Errorf("%v: exit %d, want usage", args, code)
		}
	}
}
