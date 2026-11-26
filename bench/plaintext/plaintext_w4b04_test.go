package plaintext

import (
	"strings"
	"testing"
)

// TestCleanKeepsComposeKeys: a compose file key such as x-heimdall
// build_network is what the person must edit, so the command rule never
// touches it; a real command line is still replaced (wave 4, F1.3, group B
// review).
func TestCleanKeepsComposeKeys(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`a build gets the network only with x-heimdall build_network`, `a build gets the network only with x-heimdall build_network`},
		{`services a, b are all marked x-heimdall workspace; mark exactly one`, `services a, b are all marked x-heimdall workspace; mark exactly one`},
		{"live scoring is not configured: run `heimdall livescore init`", "live scoring is not configured: run `the command line tool`"},
		{`no inbox items yet; run bifrost-bench inbox pull -source x`, `no inbox items yet; run the command line tool`},
	} {
		if got := Clean(tc.in); got != tc.want {
			t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCleanKeepsWhatToFix: a cleaned message keeps the benchmark name, the
// task name, the secret mask and the sentence; only the server part goes.
func TestCleanKeepsWhatToFix(t *testing.T) {
	roots := []string{"/var/lib/heimdall/runs", "/data/Heimdall/bench", "/tmp"}
	in := "task t1 of benchmark hd-swe: open /var/lib/heimdall/runs/orgs/org_01m7yyh2wft8r2x5tjyxthksbj/benchmarks/hd-swe/tasks/t1/task.yaml: no such file or directory; key sk-***redacted*** refused"
	got := Clean(in, roots...)
	for _, keep := range []string{"task t1 of benchmark hd-swe", "tasks/t1/task.yaml does not exist", "sk-***redacted***", "refused"} {
		if !strings.Contains(got, keep) {
			t.Errorf("Clean lost %q: %q", keep, got)
		}
	}
	for _, gone := range []string{"/var/", "org_01m7", "/data/"} {
		if strings.Contains(got, gone) {
			t.Errorf("Clean kept %q: %q", gone, got)
		}
	}
}

// TestCleanDropsChainedFlags: a command line hint with several flags loses
// all of them, not only the first (group B review).
func TestCleanDropsChainedFlags(t *testing.T) {
	got := Clean("spend cap reached; raise the cap with run -resume e_x -total-usd 5")
	if strings.Contains(got, "-resume") || strings.Contains(got, "-total-usd") || !strings.HasPrefix(got, "spend cap reached") {
		t.Fatalf("Clean = %q", got)
	}
	if got := Clean("agents/x.yaml: field model: use -model or set it"); got != "agents/x.yaml: field model: set it" {
		t.Fatalf("single flag: %q", got)
	}
}
