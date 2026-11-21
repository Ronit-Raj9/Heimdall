//go:build integration

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// agentITImages are the local images the isolated stack and the check need.
// Nothing is pulled: the host's DNS is not trusted in tests.
var agentITImages = []string{
	"bench/coding-core-base:1",
	"alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
	"postgres:18@sha256:979bccfde35445db618c65107d27d08c6c21339d8dc3ad4c830c6633947be0e4",
	"maximhq/bifrost:v2.5.1@sha256:5ec9873acaf1687556b6e4dd854b92b539d9debf87a5a683b66338648b84d3c8",
}

// TestIntegrationAgentCheck runs the custom-agent path end to end on an
// isolated, keyless copy of the deploy stack (its own compose project,
// network names and loopback ports; the fault proxy is this tree's binary
// in a pinned alpine image): agent init -template python, agent build on
// bench/coding-core-base:1, then agent check against coding-core t01, which
// must pass every row; the command and http-service templates must pass
// every row too. A deliberately broken agent (no model call, a
// non-zero exit, an egress attempt) must fail exactly the rows for those.
// The stack, the built image and every run container are removed at the end.
func TestIntegrationAgentCheck(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	for _, img := range agentITImages {
		if exec.CommandContext(ctx, "docker", "image", "inspect", img).Run() != nil {
			t.Skipf("local image %s is missing", img)
		}
	}
	st := startAgentITStack(ctx, t)
	root := t.TempDir()
	// Sealed run folders are read-only; make them removable for TempDir.
	t.Cleanup(func() {
		if b, err := exec.Command("chmod", "-R", "u+w", root).CombinedOutput(); err != nil {
			t.Logf("chmod %s: %v %s", root, err, b)
		}
	})
	cfgPath := st.writeBenchConfig(t, root)
	t.Setenv("BIFROST_SETUP_TOKEN", st.setupToken)
	t.Setenv("BENCH_FAULTPROXY_TOKEN", st.proxyToken)
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "BENCH_MODEL", "BENCH_OPENAI_MODEL", "BENCH_GATEWAY_FAULT_PROXY_URL"} {
		t.Setenv(k, "")
	}

	suffix := agentITHex(t, 3)
	for i, tmpl := range []string{"python", "command", "http-service"} {
		t.Run(tmpl, func(t *testing.T) {
			checkTemplate(ctx, t, st, root, cfgPath, tmpl, "hcheck"+suffix+fmt.Sprint(i), i == 0)
		})
	}

	broken := filepath.Join(root, "agents", "broken.yaml")
	agentITWrite(t, broken, brokenAgentYAML)
	rep := agentCheck(ctx, checkOptions{config: cfgPath, agent: broken, suite: "coding-core", task: "t01", runs: "checks"}, io.Discard)
	logReport(t, rep)
	want := map[string]string{
		rowImage: checkPass, rowUser: checkPass, rowModelCall: checkFail, rowSession: checkWarn,
		rowExit: checkFail, rowDiff: checkPass, rowEgress: checkFail, rowEvidence: checkPass, rowClaim: checkPass,
		rowProcObserver: checkPass, rowFSObserver: checkWarn, // the broken agent writes no file
	}
	if rep.Result != checkFail || checkReportExit(rep) != 1 {
		t.Errorf("broken agent: result %s exit %d, want FAIL and 1", rep.Result, checkReportExit(rep))
	}
	for _, r := range rep.Rows {
		if r.Status != want[r.ID] && !tooShortToObserve(r) {
			t.Errorf("broken agent row %s = %s, want %s: %s", r.ID, r.Status, want[r.ID], r.Detail)
		}
	}
	for _, r := range rep.Rows {
		if r.ID == rowEgress && !strings.Contains(r.Detail, "heimdall-egress.example.com") {
			t.Errorf("egress row does not name the host the agent tried: %s", r.Detail)
		}
	}
}

// checkTemplate runs agent init, agent build and agent check for one
// template and expects every row to pass. With build it is agent build that
// builds the image; otherwise the check itself builds it.
func checkTemplate(ctx context.Context, t *testing.T, st *agentITStack, root, cfgPath, tmpl, id string, build bool) {
	tag := "heimdall-test/agent-" + id + ":1"
	var out bytes.Buffer
	if code := runAgentInit([]string{id, "-template", tmpl, "-tag", tag, "-config", cfgPath}, &out); code != exitOK {
		t.Fatalf("agent init: exit %d\n%s", code, out.String())
	}
	built := tag + "-coding-core"
	t.Cleanup(func() { agentITDocker(t, "rmi", "-f", built) })
	agentPath := filepath.Join(root, "agents", id+".yaml")
	if build {
		out.Reset()
		if code := runAgentBuild([]string{agentPath, "-suite", "coding-core", "-config", cfgPath}, &out); code != exitOK {
			t.Fatalf("agent build: exit %d\n%s", code, out.String())
		}
		if !strings.Contains(out.String(), "tag    "+built+"\n") || !strings.Contains(out.String(), "image  sha256:") {
			t.Fatalf("agent build output:\n%s", out.String())
		}
	}
	rep := agentCheck(ctx, checkOptions{config: cfgPath, agent: agentPath, suite: "coding-core", task: "t01", runs: "checks", noBuild: build}, io.Discard)
	logReport(t, rep)
	// The scripted model asks for no edit, so a template agent writes no
	// file and the workspace observer row is a WARN that says so.
	noFileEvent := func(r checkRow) bool {
		return r.ID == rowFSObserver && r.Status == checkWarn && strings.Contains(r.Detail, "legitimately shows none")
	}
	if (rep.Result != checkPass && rep.Result != checkWarn) || rep.Image != built {
		t.Errorf("template %s: result %s image %s, want PASS on %s", tmpl, rep.Result, rep.Image, built)
	}
	for _, r := range rep.Rows {
		if r.Status != checkPass && !noFileEvent(r) && !tooShortToObserve(r) {
			t.Errorf("template %s row %s = %s: %s", tmpl, r.ID, r.Status, r.Detail)
		}
	}
	if !strings.HasPrefix(rep.RunDir, filepath.Join(root, "checks")) {
		t.Errorf("run sealed in %s, want under %s", rep.RunDir, filepath.Join(root, "checks"))
	}
	st.checkScriptedLedger(ctx, t, rep.RunID)
}

// brokenAgentYAML makes no model call, tries to reach the internet and
// exits 3. It runs in the suite's base image with no build.
const brokenAgentYAML = `id: broken
adapter: command
image: bench/coding-core-base:1
argv:
  - python3
  - -c
  - |
    import sys, urllib.request
    try:
        urllib.request.urlopen("http://heimdall-egress.example.com/telemetry", timeout=5)
    except Exception as e:
        print("telemetry failed:", e, file=sys.stderr)
    print("I could not finish the task.")
    sys.exit(3)
model: claude-sonnet-4-5
notes:
  project_file: AGENTS.md
budgets:
  wall_s: 120
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
  max_retries: 0
`

// agentITStack is one running isolated copy of deploy/docker-compose.yml.
type agentITStack struct {
	project, dir, setupToken, proxyToken string
	apiPort, ctlPort                     int
	compose                              func(ctx context.Context, args ...string) ([]byte, error)
}

// startAgentITStack builds this tree's binary for the fault proxy and starts
// the deploy compose file under a new project name with renamed networks,
// free loopback ports, empty provider keys and a clean environment, so
// nothing can reach a real provider and nothing of the live stack is
// touched.
func startAgentITStack(ctx context.Context, t *testing.T) *agentITStack {
	t.Helper()
	benchRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	st := &agentITStack{project: "heimdallit" + agentITHex(t, 4), dir: t.TempDir(),
		setupToken: "it-" + agentITHex(t, 16), proxyToken: "fp-" + agentITHex(t, 16),
		apiPort: agentITPort(t), ctlPort: agentITPort(t)}
	bin := filepath.Join(st.dir, "bifrost-bench")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/bifrost-bench")
	build.Dir, build.Env = benchRoot, append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build bench binary: %v\n%s", err, b)
	}
	agentITWrite(t, filepath.Join(st.dir, "override.yaml"), st.override(bin))
	agentITWrite(t, filepath.Join(st.dir, "stack.env"), fmt.Sprintf("BIFROST_SETUP_TOKEN=%s\nBIFROST_PG_PASSWORD=pg-%s\nBENCH_FAULTPROXY_TOKEN=%s\n",
		st.setupToken, agentITHex(t, 12), st.proxyToken))
	st.compose = func(ctx context.Context, args ...string) ([]byte, error) {
		base := []string{"compose", "-p", st.project, "-f", filepath.Join(benchRoot, "deploy", "docker-compose.yml"),
			"-f", filepath.Join(st.dir, "override.yaml"), "--env-file", filepath.Join(st.dir, "stack.env")}
		cmd := exec.CommandContext(ctx, "docker", append(base, args...)...)
		cmd.Env = agentITCleanEnv()
		return cmd.CombinedOutput()
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer ccancel()
		if b, err := st.compose(cctx, "down", "-v", "--remove-orphans"); err != nil {
			t.Errorf("compose down: %v\n%s", err, b)
		}
	})
	if b, err := st.compose(ctx, "up", "-d", "--wait", "--wait-timeout", "240"); err != nil {
		logs, _ := st.compose(ctx, "logs", "--tail", "40")
		t.Fatalf("compose up: %v\n%s\n%s", err, b, logs)
	}
	return st
}

// override is the compose override: renamed networks, a keyless fault proxy
// running this tree's binary, and free loopback ports.
func (st *agentITStack) override(bin string) string {
	keys := []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "MISTRAL_API_KEY", "XAI_API_KEY",
		"OPENROUTER_API_KEY", "FIREWORKS_API_KEY", "DEEPSEEK_API_KEY", "CEREBRAS_API_KEY", "PERPLEXITY_API_KEY", "SARVAM_API_KEY"}
	var env strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&env, "      %s: \"\"\n", k)
	}
	return fmt.Sprintf(`networks:
  evalnet:
    name: %[1]s-evalnet
  upstream:
    name: %[1]s-upstream
services:
  faultproxy:
    image: %[2]s
    user: "65534:65534"
    command: ["/usr/local/bin/bifrost-bench", "proxy"]
    environment:
      BENCH_FAULTPROXY_LEDGER: /tmp/ledger.jsonl
      BENCH_FAULTPROXY_PROVIDERS: ""
%[3]s    volumes: !override
      - %[4]s:/usr/local/bin/bifrost-bench:ro
    ports: !override
      - "127.0.0.1:%[5]d:9101"
    restart: "no"
  bifrost:
    ports: !override
      - "127.0.0.1:%[6]d:8080"
    restart: "no"
  postgres:
    restart: "no"
`, st.project, agentITImages[1], env.String(), bin, st.ctlPort, st.apiPort)
}

// writeBenchConfig writes <root>/deploy/bench.yaml pointing at the isolated
// stack, with runs and checks under root and the tree's suites read in place.
func (st *agentITStack) writeBenchConfig(t *testing.T, root string) string {
	t.Helper()
	benchRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`runs_root: runs
benchmarks_root: %s
pricing: %s
server:
  listen: 127.0.0.1:1
  mode: live
gateway:
  url: http://127.0.0.1:%d
  agent_url: http://bifrost:8080
  admin_token_env: BIFROST_SETUP_TOKEN
  provider: anthropic
  key_slots: [fp-slot-1, fp-slot-2, fp-slot-3, fp-slot-4]
faultproxy:
  control_url: http://127.0.0.1:%d
networks:
  eval: %s-evalnet
images:
  helper: bench/coding-core-base:1
`, filepath.Join(benchRoot, "benchmarks"), filepath.Join(benchRoot, "deploy", "pricing-snapshot.json"), st.apiPort, st.ctlPort, st.project)
	p := filepath.Join(root, "deploy", "bench.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	agentITWrite(t, p, cfg)
	return p
}

// checkScriptedLedger asserts every ledger line of the run was answered by
// the scripted model.
func (st *agentITStack) checkScriptedLedger(ctx context.Context, t *testing.T, runID string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/ledger?run_id=%s", st.ctlPort, runID), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"upstream":"scripted"`) == 0 || strings.Contains(string(b), `"upstream":"real"`) {
		t.Errorf("ledger of %s is not all scripted: %s", runID, b)
	}
}

func logReport(t *testing.T, rep *checkReport) {
	t.Helper()
	var b bytes.Buffer
	printCheckReport(&b, rep)
	t.Log("\n" + b.String())
}

// agentITCleanEnv is the environment compose runs with: enough to reach
// docker, and no provider key from the developer's shell.
func agentITCleanEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "PATH", "HOME", "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CONTEXT", "XDG_RUNTIME_DIR", "LANG", "TMPDIR":
			out = append(out, kv)
		}
	}
	return out
}

func agentITDocker(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if b, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		t.Logf("docker %s: %v %s", strings.Join(args, " "), err, b)
	}
}

func agentITPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func agentITHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func agentITWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// tooShortToObserve reports a process observer row that warns because the
// agent exited (in well under a second here) before the observer could
// join its PID namespace: honest missing evidence, not a broken observer.
func tooShortToObserve(r checkRow) bool {
	return r.ID == rowProcObserver && r.Status == checkWarn && strings.Contains(r.Detail, "before the process observer could join")
}
