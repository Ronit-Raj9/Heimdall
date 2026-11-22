package ci

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/verdictab"
)

func rep(winner string, sig bool, liveScore, prScore, liveCost, prCost float64) *reportcard.Report {
	return &reportcard.Report{
		Evaluation: "ev_x",
		Benchmarks: []reportcard.Benchmark{{Benchmark: "b1",
			Arms: map[string]reportcard.Metrics{
				ArmLive: {Score: liveScore, Runs: 10, CostUSD: &liveCost},
				ArmPR:   {Score: prScore, Runs: 10, CostUSD: &prCost},
			},
			Verdict: &verdictab.BenchmarkVerdict{Winner: winner, Significant: sig, Delta: prScore - liveScore}}},
		Overall: &verdictab.OverallVerdict{Winner: winner, Significant: sig, Delta: prScore - liveScore, Benchmarks: 1,
			Wins: map[string]int{winner: 1}},
	}
}

func TestEvaluateGate(t *testing.T) {
	def := GateOptions{FailOn: DefaultFailOn}
	cases := []struct {
		name    string
		status  string
		r       *reportcard.Report
		o       GateOptions
		outcome string
		code    int
	}{
		{"improved", "done", rep("B", true, 0.4, 0.6, 1, 1), def, OutcomeImproved, 0},
		{"regressed", "done", rep("A", true, 0.6, 0.4, 1, 1), def, OutcomeRegressed, GateExitRegressed},
		{"unclear passes by default", "done", rep("unclear", false, 0.5, 0.45, 1, 1), def, OutcomeInconclusive, 0},
		{"unclear fails when listed", "done", rep("unclear", false, 0.5, 0.45, 1, 1), GateOptions{FailOn: []string{OutcomeInconclusive}}, OutcomeInconclusive, GateExitInconclusive},
		{"tie", "done", rep("tie", false, 0.5, 0.5, 1, 1), def, OutcomeSame, 0},
		{"score drop limit", "done", rep("unclear", false, 0.5, 0.4, 1, 1), GateOptions{FailOn: DefaultFailOn, MaxScoreDrop: 0.05}, OutcomeRegressed, GateExitRegressed},
		{"cost limit", "done", rep("B", true, 0.4, 0.6, 1, 2), GateOptions{FailOn: DefaultFailOn, MaxCostIncrease: 0.25}, OutcomeRegressed, GateExitRegressed},
		{"failed", "failed", rep("B", true, 0.4, 0.6, 1, 1), def, OutcomeFailed, GateExitFailed},
		{"no runs", "done", &reportcard.Report{}, def, OutcomeFailed, GateExitFailed},
	}
	for _, c := range cases {
		g := EvaluateGate(c.status, c.r, c.o)
		if g.Outcome != c.outcome || g.ExitCode != c.code || g.Pass != (c.code == 0) {
			t.Errorf("%s: %+v", c.name, g)
		}
	}
}

func TestParseFailOn(t *testing.T) {
	if got, err := ParseFailOn(" regressed, failed ,"); err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ParseFailOn("invalid"); err == nil {
		t.Fatal("unknown outcome accepted")
	}
}

func TestEvaluationComment(t *testing.T) {
	r := rep("A", true, 0.6, 0.4, 2, 3)
	r.Reliability = []reportcard.Reliability{{Fault: "model-down", Arms: map[string]reportcard.FaultScore{ArmLive: {Score: 0.5, Drop: -0.1}, ArmPR: {Score: 0.2, Drop: -0.2}}}}
	g := EvaluateGate("done", r, GateOptions{FailOn: DefaultFailOn})
	s := EvaluationComment(CommentInput{Agent: "my-agent", LiveVersion: 3, PRVersion: 5, Report: r, Gate: g, ReportURL: "https://h/evaluations/ev_x", Commit: "0123456789abcdef"})
	for _, want := range []string{EvaluationMarker("my-agent"), "v5 (this pull request) vs v3 (live)", "Gate failed", "| b1 | 60%", "**worse**", "-20 pts", "$2.00 | $3.00 | +50%", "model-down", "[Open the full report card](https://h/evaluations/ev_x)", "`0123456789ab`"} {
		if !strings.Contains(s, want) {
			t.Errorf("comment lacks %q:\n%s", want, s)
		}
	}
	if strings.ContainsAny(s, "\u2014\u2013") {
		t.Error("em or en dash in the comment")
	}
	if d := StatusDescription(g); len(d) > 140 || !strings.HasPrefix(d, "Worse") {
		t.Errorf("status %q", d)
	}
}

func TestHostedWorkflows(t *testing.T) {
	o := HostedOptions{Agent: "my-agent", Config: "agents/my-agent.yaml", Pack: "coding", Faults: []string{"model-down"}}
	gh, err := GitHubHostedWorkflow(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"$base/downloads/$asset"`, "sha256sum -c", "run: heimdall ci evaluate", "HEIMDALL_TOKEN: ${{ secrets.HEIMDALL_TOKEN }}", "HEIMDALL_URL: ${{ vars.HEIMDALL_URL }}",
		`HEIMDALL_AGENT: "my-agent"`, `HEIMDALL_CONFIG: "agents/my-agent.yaml"`, `HEIMDALL_PACK: "coding"`, `HEIMDALL_FAULTS: "model-down"`, `HEIMDALL_FAIL_ON: "regressed,failed"`,
		`dir="$RUNNER_TEMP/heimdall-bin"`, `echo "$dir" >> "$GITHUB_PATH"`, "GITHUB_TOKEN: ${{ github.token }}", uploadAction,
		"statuses: write", "pull-requests: write", "head.repo.full_name == github.repository"} {
		if !strings.Contains(gh, want) {
			t.Errorf("github workflow lacks %q:\n%s", want, gh)
		}
	}
	// Nothing is built from or fetched off GitHub: no Heimdall action, no clone.
	for _, bad := range []string{"Ronit-Raj9", "bench/ci@", "git clone", "go build", "raw.githubusercontent.com"} {
		if strings.Contains(gh, bad) {
			t.Errorf("github workflow still has %q", bad)
		}
	}
	o.Image, o.Config = true, ""
	ghImage, err := GitHubHostedWorkflow(o)
	if err != nil || !strings.Contains(ghImage, "HEIMDALL_IMAGE: ${{ steps.image.outputs.digest }}") {
		t.Errorf("image workflow: %v\n%s", err, ghImage)
	}
	o.Image, o.Config = false, "agents/my-agent.yaml"
	o.URL = "https://heimdall.example.com/"
	gl, err := GitLabHostedCI(o)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"merge_request_event", `HEIMDALL_URL: "https://heimdall.example.com"`, `HEIMDALL_AGENT: "my-agent"`, "heimdall ci evaluate", "GITLAB_TOKEN",
		"image: alpine:3.23", `"$base/downloads/$asset"`, "dir=/usr/local/bin"} {
		if !strings.Contains(gl, want) {
			t.Errorf("gitlab lacks %q:\n%s", want, gl)
		}
	}
	for _, bad := range []string{"Ronit-Raj9", "github.com", "go build", "golang:"} {
		if strings.Contains(gl, bad) {
			t.Errorf("gitlab file still has %q", bad)
		}
	}
	if strings.Contains(gl, "hm_") || strings.Contains(gh, "hm_") {
		t.Error("a token in a workflow")
	}
	for _, bad := range []HostedOptions{
		{Agent: "Bad Agent", Config: "a.yaml", Pack: "coding"},
		{Agent: "a", Config: "../x.yaml", Pack: "coding"},
		{Agent: "a", Config: "a.yaml"},
		{Agent: "a", Pack: "coding"},
		{Agent: "a", Config: "a.yaml", Pack: "coding", URL: "ftp://x"},
		{Agent: "a", Config: "a.yaml", Pack: "coding", FailOn: "invalid"},
	} {
		if _, err := GitHubHostedWorkflow(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if _, _, err := HostedWorkflow("bitbucket", o); err == nil {
		t.Error("unknown provider accepted")
	}
}

// TestGitLabTemplateDownloadsFromServer: the template the server serves at
// /downloads/heimdall.gitlab-ci.yml runs the same checked download as the
// generated workflows, with an empty HEIMDALL_URL the server fills in.
func TestGitLabTemplateDownloadsFromServer(t *testing.T) {
	tmpl := string(GitLabTemplate())
	for _, line := range strings.Split(strings.ReplaceAll(DownloadScript, `dir="$1"`, `dir=/usr/local/bin`), "\n") {
		if !strings.Contains(tmpl, "      "+line+"\n") {
			t.Errorf("template lacks download line %q", line)
		}
	}
	for _, want := range []string{".heimdall-evaluate:", `HEIMDALL_URL: ""`, "heimdall ci evaluate -out heimdall-report", "/downloads/heimdall.gitlab-ci.yml"} {
		if !strings.Contains(tmpl, want) {
			t.Errorf("template lacks %q", want)
		}
	}
	for _, bad := range []string{"Ronit-Raj9", "raw.githubusercontent.com", "go build", "\u2014", "\u2013"} {
		if strings.Contains(tmpl, bad) {
			t.Errorf("template has %q", bad)
		}
	}
}

// TestDownloadScriptRuns runs the download script against a fake server
// folder: a good binary installs, a tampered one fails the checksum.
func TestDownloadScriptRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	srv := t.TempDir()
	asset := ""
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		asset = "bifrost-bench-linux-amd64"
	case "linux/arm64":
		asset = "bifrost-bench-linux-arm64"
	default:
		t.Skip("the test serves a shell script as the binary on linux only")
	}
	bin := []byte("#!/bin/sh\necho 0.1.0\n")
	if err := os.MkdirAll(filepath.Join(srv, "downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(srv, "downloads", asset), bin, 0o644)
	sum := sha256.Sum256(bin)
	os.WriteFile(filepath.Join(srv, "downloads", "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+asset+"\n"), 0o644)
	hs := httptest.NewServer(http.FileServer(http.Dir(srv)))
	defer hs.Close()
	run := func() (string, error) {
		dst := t.TempDir()
		cmd := exec.Command("sh", "-c", DownloadScript, "sh", dst)
		cmd.Env = append(os.Environ(), "HEIMDALL_URL="+hs.URL+"/")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(); err != nil || !strings.Contains(out, "0.1.0") {
		t.Fatalf("good download: %v\n%s", err, out)
	}
	os.WriteFile(filepath.Join(srv, "downloads", asset), append(bin, '#'), 0o644)
	if out, err := run(); err == nil {
		t.Fatalf("tampered binary installed:\n%s", out)
	}
}

// TestActionEvaluateMode: the action defaults to the hosted evaluation, has
// no fixture bundle default, and hands inputs to `ci evaluate` through the
// environment, never spliced into the command line.
func TestActionEvaluateMode(t *testing.T) {
	raw, err := os.ReadFile("action.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"evaluate|recorded|live) ;;", `"$BIN" ci evaluate || code=$?`, "HEIMDALL_TOKEN: ${{ inputs.token }}", "recorded mode needs the bundle input"} {
		if !strings.Contains(s, want) {
			t.Errorf("action.yml lacks %q", want)
		}
	}
	if strings.Contains(s, "default: testdata/recorded") {
		t.Error("action.yml still defaults to the fixture bundle")
	}
	if strings.Contains(s, "ci evaluate -") {
		t.Error("an input is spliced into the ci evaluate command line")
	}
}

// TestGitLabTemplate keeps the checked-in GitLab template runnable.
func TestGitLabTemplate(t *testing.T) {
	raw, err := os.ReadFile("heimdall.gitlab-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".heimdall-evaluate:", "merge_request_event", "heimdall ci evaluate", "SHA256SUMS"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("template lacks %q", want)
		}
	}
}
