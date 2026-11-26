package plaintext

import (
	"errors"
	"strings"
	"testing"
)

const testRunsRoot = "/var/lib/heimdall/runs"

// TestCleanExamples pins the cleaned forms of the tester messages of wave 4
// (F1.3, p13-8) and the texts that must pass through unchanged.
func TestCleanExamples(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "p09-3 import of a file where a folder is wanted",
			in: "The import failed: read /var/lib/heimdall/runs/projects/org_01m7yyh2wft8r2x5tjyxthksbj/uploads/datasets/import-msibench/zz-test-p09-bad.csv/index/cases.jsonl: " +
				"open /var/lib/heimdall/runs/projects/org_01m7yyh2wft8r2x5tjyxthksbj/uploads/datasets/import-msibench/zz-test-p09-bad.csv/index/cases.jsonl: not a directory " +
				"(want the MSI-Bench v2 release package: download v2/ of the dataset)",
			want: "The import failed: read uploads/datasets/import-msibench/zz-test-p09-bad.csv/index/cases.jsonl: that is a file, not a folder " +
				"(want the MSI-Bench v2 release package: download v2/ of the dataset)",
		},
		{
			name: "p13-6 schedule with a missing experiment file",
			in:   "Not saved: schedule zz-test-p13-sched1: experiment file: stat /var/lib/heimdall/runs/projects/org_01m7yyh2wft8r2x5tjyxthksbj/experiments/zz-test-p13.yaml: no such file or directory",
			want: "Not saved: schedule zz-test-p13-sched1: experiment file: experiments/zz-test-p13.yaml does not exist",
		},
		{
			name: "p13-6 task type flag hint",
			in:   "suite.yaml was written from template qa, not tool-use; pass -kind qa or leave -kind out",
			want: "suite.yaml was written from template qa, not tool-use; choose qa or leave the setting on Automatic",
		},
		{
			name: "p06 gateway slot in a run step",
			in:   "slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20: 503 from conversation call 2",
			want: "provider slot openai 20: 503 from conversation call 2",
		},
		{
			name: "p06 index job log line outside the known roots",
			in:   "results index (jsonl) /srv/heimdall/data/.index/results.jsonl",
			want: "results index (jsonl) a file on the server",
		},
		{
			name: "absolute path inside an organization folder keeps its last two parts",
			in:   "cannot read /srv/other/orgs/org_01m7yyh2wft8r2x5tjyxthksbj/a/b/c.json",
			want: "cannot read b/c.json",
		},
		{
			name: "known root with an organization project folder",
			in:   "open /var/lib/heimdall/runs/projects/org_x/experiments/a.yaml: permission denied",
			want: "open experiments/a.yaml: permission denied",
		},
		{
			name: "bare organization id",
			in:   "organization org_01m7yyh2wft8r2x5tjyxthksbj has no key",
			want: "organization your organization has no key",
		},
		{
			name: "canary plan stutter",
			in:   "plan: plan: plan e_canary_zz_test_p13_drift1_y8zi: arm A: condition clean, task bfcl-irrelevance-0: boom",
			want: "arm A: condition clean, task bfcl-irrelevance-0: boom",
		},
		{
			name: "command line tool and its flags",
			in:   "no inbox items yet; run bifrost-bench inbox pull -all",
			want: "no inbox items yet; run the command line tool",
		},
		{
			name: "flag after use",
			in:   "the lock is stale, use -force to replace it",
			want: "the lock is stale, replace it",
		},
		{name: "url unchanged", in: "see https://x/y for more", want: "see https://x/y for more"},
		{name: "relative path unchanged", in: "experiments/a.yaml is not valid YAML", want: "experiments/a.yaml is not valid YAML"},
		{name: "hyphen in prose unchanged", in: "press re-run to try again", want: "press re-run to try again"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Clean(c.in, testRunsRoot); got != c.want {
				t.Errorf("Clean(%q)\n got  %q\n want %q", c.in, got, c.want)
			}
		})
	}
}

// TestCleanLongestRootFirst checks that a nested root is stripped as a whole.
func TestCleanLongestRootFirst(t *testing.T) {
	got := Clean("read /app/bench/runs/x.json failed", "/app/bench", "/app/bench/runs")
	if got != "read x.json failed" {
		t.Fatalf("got %q", got)
	}
}

// TestHostedOnlyWhenRootsSet checks that local mode and the CLI keep the full text.
func TestHostedOnlyWhenRootsSet(t *testing.T) {
	msg := "stat /var/lib/heimdall/runs/projects/org_01m7yyh2wft8r2x5tjyxthksbj/x.yaml: no such file or directory"
	saved := DefaultRoots
	t.Cleanup(func() { DefaultRoots = saved })
	DefaultRoots = nil
	if got := Hosted(msg); got != msg {
		t.Fatalf("local mode changed the text: %q", got)
	}
	DefaultRoots = func() []string { return []string{testRunsRoot} }
	got := Hosted(msg)
	if strings.Contains(got, "/var/") || strings.Contains(got, "org_") {
		t.Fatalf("hosted text still names the server: %q", got)
	}
	if CleanErr(errors.New(msg), testRunsRoot) != got {
		t.Fatalf("CleanErr differs from Clean")
	}
}
