package main

import (
	"path/filepath"
	"testing"
)

// TestRunFlagsAfterFile covers 7-C-01: the documented example command puts
// -config after the experiment path. It must get past argument parsing (to
// the missing-config error, exit 4) instead of a usage error (64).
func TestRunFlagsAfterFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "deploy", "bench.yaml")
	for name, run := range map[string]func([]string) int{"run": runRun, "plan": runPlan, "check": runCheck} {
		if got := run([]string{"experiments/visual-smoke.yaml", "-config", missing}); got != exitInfra {
			t.Errorf("%s with -config after the file: exit %d, want %d", name, got, exitInfra)
		}
		if got := run([]string{"a.yaml", "b.yaml", "-config", missing}); got != exitUsage {
			t.Errorf("%s with two files: exit %d, want %d", name, got, exitUsage)
		}
	}
}
