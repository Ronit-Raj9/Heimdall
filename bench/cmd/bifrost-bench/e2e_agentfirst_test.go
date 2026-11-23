//go:build e2eagentfirst

// End-to-end test of the agent-first flow (audit/AGENTFIRST_CONTRACT.md,
// lane A22). It starts the real `serve` command in this process over a
// temporary project with two small admitted benchmarks (coding-core from
// benchmarks/ and acme-memory from examples/acme-memory) and the scripted
// oracle agent, then walks the contract's routes:
//
//	GET  /agents/{id}/compatibility
//	POST /agents/{id}/versions, PUT /agents/{id}/config (v1 and v2)
//	POST /evaluations/estimate
//	POST /evaluations (mode versions: oracle@v1 vs oracle@v2)
//	GET  /evaluations/{id} until done
//	GET  /evaluations/{id}/report
//	GET  /leaderboard/agents
//
// The scripted agents make no model calls, so nothing is spent. The run
// half needs Docker, the bench images and the gateway stack (`make up`);
// without them the server starts -offline and only the planning half runs.
// Set BENCH_E2E_REQUIRE_STACK=1 to fail instead of skipping the run half.
//
// Run with `make e2e-agentfirst` (build tag e2eagentfirst), so normal CI
// never builds it.
package main

import (
	"bytes"
	"encoding/json"
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

const (
	e2eToken  = "e2e-agentfirst-token"
	e2eAgent  = "oracle"
	e2eModel  = "claude-sonnet-4-5"
	e2eCoding = "coding-core"
	e2eMemory = "acme-memory"
)

// knownCapabilities is the report card's capability list (contract section 4).
var knownCapabilities = map[string]bool{
	"terminal": true, "coding": true, "memory": true, "support": true, "research": true,
	"voice": true, "safety": true, "team": true, "gpu": true,
}

type e2eClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *e2eClient) do(method, path string, body any) (int, map[string]any, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("encode %s %s: %v", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+"/api/bench"+path, rd)
	if err != nil {
		c.t.Fatalf("request %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+e2eToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, raw
}

// must answers the body of a request that has to succeed with one of want.
func (c *e2eClient) must(method, path string, body any, want ...int) map[string]any {
	c.t.Helper()
	code, out, raw := c.do(method, path, body)
	for _, w := range want {
		if code == w {
			if out == nil {
				c.t.Fatalf("%s %s: %d but body is not a JSON object: %s", method, path, code, raw)
			}
			return out
		}
	}
	c.t.Fatalf("%s %s: status %d, want %v: %s", method, path, code, want, raw)
	return nil
}

func TestE2EAgentFirst(t *testing.T) {
	benchRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	project := e2eProject(t, benchRoot)
	live, why := e2eStackReady()
	if !live && os.Getenv("BENCH_E2E_REQUIRE_STACK") == "1" {
		t.Fatalf("BENCH_E2E_REQUIRE_STACK=1 but the stack is not ready: %s", why)
	}
	c := e2eServe(t, project, live)

	// 1. Two frozen versions of the agent: v1 is the oracle as written, v2 is
	// the same file switched to the noop control, so v1 must beat v2.
	v1 := e2eSnapshot(t, c, "e2e v1 oracle")
	cfg := c.must("GET", "/agents/"+e2eAgent+"/config", nil, 200)
	yaml, _ := cfg["yaml"].(string)
	if !strings.Contains(yaml, "kind: oracle") {
		t.Fatalf("agent yaml has no scripted oracle kind:\n%s", yaml)
	}
	c.must("PUT", "/agents/"+e2eAgent+"/config", map[string]any{
		"yaml":     strings.Replace(yaml, "kind: oracle", "kind: noop", 1),
		"if_match": cfg["sha256"],
	}, 200)
	v2 := e2eLatestVersion(t, c)
	if v2 <= v1 {
		// The save should have snapshotted on its own (contract section 2);
		// snapshot explicitly so the rest still runs, but say so.
		t.Errorf("PUT /agents/%s/config made no new version (latest %d, v1 %d)", e2eAgent, v2, v1)
		v2 = e2eSnapshot(t, c, "e2e v2 noop")
	}
	if v2 <= v1 {
		t.Fatalf("v2 %d is not newer than v1 %d", v2, v1)
	}
	ver := c.must("GET", fmt.Sprintf("/agents/%s/versions/%d", e2eAgent, v1), nil, 200)
	if y, _ := ver["yaml"].(string); !strings.Contains(y, "kind: oracle") {
		t.Errorf("version %d is not the frozen oracle config: %v", v1, ver)
	}

	// 2. Compatibility: both benchmarks listed, both runnable by this agent.
	compat := c.must("GET", "/agents/"+e2eAgent+"/compatibility", nil, 200)
	if compat["agent"] != e2eAgent {
		t.Errorf("compatibility agent = %v, want %s", compat["agent"], e2eAgent)
	}
	if _, ok := compat["capabilities"].([]any); !ok {
		t.Errorf("compatibility has no capabilities list: %v", compat)
	}
	rows := e2eByKey(t, compat["benchmarks"], "id")
	for _, id := range []string{e2eCoding, e2eMemory} {
		row, ok := rows[id]
		if !ok {
			t.Fatalf("compatibility lists no benchmark %s: %v", id, compat["benchmarks"])
		}
		if row["ok"] != true {
			t.Errorf("benchmark %s not compatible: %v", id, row)
		}
		for _, k := range []string{"requires", "missing", "reason"} {
			if _, ok := row[k]; !ok {
				t.Errorf("compatibility row %s has no %q: %v", id, k, row)
			}
		}
	}

	spec := map[string]any{
		"name": "e2e agent-first v1 vs v2",
		"mode": "versions",
		"arms": []map[string]any{
			{"label": "A", "agent": e2eAgent, "version": v1, "model": e2eModel},
			{"label": "B", "agent": e2eAgent, "version": v2, "model": e2eModel},
		},
		"benchmarks":          []string{e2eCoding, e2eMemory},
		"tasks_per_benchmark": 2,
		"trials":              1,
		"faults":              []string{"clean"},
	}

	// 3. Estimate: 2 benchmarks x 2 tasks x 1 trial x 2 arms.
	est := c.must("POST", "/evaluations/estimate", spec, 200)
	if runs, _ := est["runs"].(float64); runs != 8 {
		t.Errorf("estimate runs = %v, want 8: %v", est["runs"], est)
	}
	cost, _ := est["est_cost_usd"].(map[string]any)
	if cost == nil {
		t.Errorf("estimate has no est_cost_usd: %v", est)
	} else if _, ok := cost["known"].(bool); !ok {
		t.Errorf("est_cost_usd has no known flag: %v", cost)
	}
	mins, _ := est["est_minutes"].(map[string]any)
	if mins == nil || num(mins["low"]) > num(mins["high"]) {
		t.Errorf("est_minutes missing or low > high: %v", est["est_minutes"])
	}
	if pb, _ := est["per_benchmark"].([]any); len(pb) != 2 {
		t.Errorf("per_benchmark has %d rows, want 2: %v", len(pb), est["per_benchmark"])
	}

	if !live {
		t.Skipf("run half skipped: %s (start the stack with `make up`, or set BENCH_E2E_REQUIRE_STACK=1 to fail)", why)
	}

	// 4. Launch and wait.
	ev := c.must("POST", "/evaluations", spec, 202)
	id, _ := ev["id"].(string)
	if id == "" {
		t.Fatalf("evaluation has no id: %v", ev)
	}
	if s, _ := ev["status"].(string); s != "queued" && s != "running" {
		t.Errorf("new evaluation status %q, want queued or running", s)
	}
	if sp, _ := ev["spec"].(map[string]any); sp == nil || sp["mode"] != "versions" {
		t.Errorf("evaluation spec mode not versions: %v", ev["spec"])
	}
	list := c.must("GET", "/evaluations", nil, 200)
	if !strings.Contains(fmt.Sprint(list), id) {
		t.Errorf("GET /evaluations does not list %s", id)
	}
	// The create answer comes before the benchmarks start (N182): the
	// children are on the evaluation once it ran.
	ev = e2eWait(t, c, id)
	if ch, _ := ev["children"].([]any); len(ch) != 2 {
		t.Errorf("evaluation has %d children, want one per benchmark (2): %v", len(ch), ev["children"])
	}
	if ev["status"] != "done" {
		t.Fatalf("evaluation %s ended %v: %v", id, ev["status"], ev)
	}
	for _, chv := range ev["children"].([]any) {
		ch, _ := chv.(map[string]any)
		exp, _ := ch["experiment_id"].(string)
		skipped, _ := ch["skipped"].(string)
		if exp == "" || skipped != "" {
			t.Errorf("child not run: %v", ch)
		}
	}

	// 5. Report card shape and the expected winner (oracle v1 over noop v2).
	rep := c.must("GET", "/evaluations/"+id+"/report", nil, 200)
	if rep["evaluation"] != id {
		t.Errorf("report evaluation = %v, want %s", rep["evaluation"], id)
	}
	arms := e2eByKey(t, rep["arms"], "label")
	if num(arms["A"]["version"]) != float64(v1) || num(arms["B"]["version"]) != float64(v2) {
		t.Errorf("report arms do not carry the versions %d and %d: %v", v1, v2, rep["arms"])
	}
	caps, _ := rep["capabilities"].([]any)
	if len(caps) == 0 {
		t.Fatalf("report has no capabilities: %v", rep)
	}
	for _, cv := range caps {
		cp, _ := cv.(map[string]any)
		if !knownCapabilities[fmt.Sprint(cp["capability"])] {
			t.Errorf("unknown capability %v", cp["capability"])
		}
		if b, _ := cp["benchmarks"].([]any); len(b) == 0 {
			t.Errorf("capability %v lists no benchmarks", cp["capability"])
		}
		e2eArmMetrics(t, fmt.Sprint("capability ", cp["capability"]), cp["arms"])
	}
	benches := e2eByKey(t, rep["benchmarks"], "benchmark")
	if len(benches) != 2 {
		t.Errorf("report has %d benchmarks, want 2", len(benches))
	}
	for _, id := range []string{e2eCoding, e2eMemory} {
		b, ok := benches[id]
		if !ok {
			t.Errorf("report has no benchmark %s", id)
			continue
		}
		if !knownCapabilities[fmt.Sprint(b["capability"])] {
			t.Errorf("benchmark %s capability %v", id, b["capability"])
		}
		if s, _ := b["experiment_id"].(string); s == "" {
			t.Errorf("benchmark %s has no experiment_id", id)
		}
		m := e2eArmMetrics(t, "benchmark "+id, b["arms"])
		if m["A"] <= m["B"] {
			t.Errorf("benchmark %s: oracle v1 score %.2f not above noop v2 %.2f", id, m["A"], m["B"])
		}
		v, _ := b["verdict"].(map[string]any)
		if v == nil {
			t.Errorf("benchmark %s has no verdict", id)
		} else if _, ok := v["summary"].(string); !ok {
			t.Errorf("benchmark %s verdict has no summary: %v", id, v)
		}
	}
	overall, _ := rep["overall"].(map[string]any)
	if overall == nil {
		t.Fatalf("report has no overall: %v", rep)
	}
	if w := overall["winner"]; w != "A" {
		t.Errorf("overall winner = %v, want A (oracle v1)", w)
	}
	if wins, _ := overall["wins"].(map[string]any); wins == nil {
		t.Errorf("overall has no wins: %v", overall)
	}
	if s, _ := overall["summary"].(string); s == "" {
		t.Errorf("overall has no summary")
	}

	// 6. Agent leaderboard carries this agent's versions.
	lb := c.must("GET", "/leaderboard/agents", nil, 200)
	found := false
	for _, rv := range firstArray(lb) {
		r, _ := rv.(map[string]any)
		if r["agent"] != e2eAgent {
			continue
		}
		found = true
		for _, k := range []string{"version", "model", "baseline", "capabilities"} {
			if _, ok := r[k]; !ok {
				t.Errorf("leaderboard row has no %q: %v", k, r)
			}
		}
		if r["baseline"] == true {
			t.Errorf("project agent %s marked baseline", e2eAgent)
		}
	}
	if !found {
		t.Errorf("leaderboard has no row for %s: %v", e2eAgent, lb)
	}
}

// e2eProject lays out a temporary project: the two benchmarks, the two
// scripted agents (with explicit capabilities), the default policy, prices.
func e2eProject(t *testing.T, benchRoot string) string {
	t.Helper()
	dir := t.TempDir()
	mem := filepath.Join(benchRoot, "examples", "acme-memory")
	copies := map[string]string{
		filepath.Join(benchRoot, "benchmarks", e2eCoding):     filepath.Join(dir, "benchmarks", e2eCoding),
		filepath.Join(mem, "benchmarks", e2eMemory):           filepath.Join(dir, "benchmarks", e2eMemory),
		filepath.Join(mem, "policies"):                        filepath.Join(dir, "policies"),
		filepath.Join(mem, "deploy", "pricing-snapshot.json"): filepath.Join(dir, "deploy", "pricing-snapshot.json"),
	}
	for src, dst := range copies {
		if err := e2eCopy(src, dst); err != nil {
			t.Fatalf("copy %s: %v", src, err)
		}
	}
	for _, name := range []string{"oracle", "noop"} {
		b, err := os.ReadFile(filepath.Join(mem, "agents", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		// Scripted controls have no default capabilities in the contract;
		// the memory benchmark needs sessions, coding-core needs shell.
		b = append(b, []byte("capabilities: [shell, tools, sessions]\n")...)
		writeE2E(t, filepath.Join(dir, "agents", name+".yaml"), b)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	writeE2E(t, filepath.Join(dir, "deploy", "bench.yaml"), []byte(fmt.Sprintf(`runs_root: runs
benchmarks_root: benchmarks
pricing: deploy/pricing-snapshot.json
server:
  listen: %s
  mode: live
  allowed_hosts: []
  cors_origins: []
gateway:
  url: http://127.0.0.1:18080
  agent_url: http://bifrost:8080
  admin_token_env: BIFROST_SETUP_TOKEN
  provider: anthropic
  key_slots: [fp-slot-1, fp-slot-2, fp-slot-3, fp-slot-4]
faultproxy:
  control_url: http://127.0.0.1:19101
networks:
  eval: bench-evalnet
images:
  helper: bench/coding-core-base:1
`, addr)))
	return dir
}

// e2eStackReady says whether the run half can run: Docker, the helper
// image, the gateway token and a listening gateway.
func e2eStackReady() (bool, string) {
	if os.Getenv("BIFROST_SETUP_TOKEN") == "" {
		return false, "BIFROST_SETUP_TOKEN is not set"
	}
	if err := exec.Command("docker", "image", "inspect", "bench/coding-core-base:1").Run(); err != nil {
		return false, "docker or image bench/coding-core-base:1 not available"
	}
	gw := os.Getenv("BENCH_GATEWAY_URL")
	if gw == "" {
		gw = "http://127.0.0.1:18080"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(gw, "http://"), "https://")
	conn, err := net.DialTimeout("tcp", strings.SplitN(host, "/", 2)[0], 2*time.Second)
	if err != nil {
		return false, "gateway " + gw + " not reachable"
	}
	conn.Close()
	return true, ""
}

// e2eServe runs `serve` in this process and waits for /healthz.
func e2eServe(t *testing.T, project string, live bool) *e2eClient {
	t.Helper()
	t.Setenv("BENCH_WRITE_TOKEN", e2eToken)
	config := filepath.Join(project, "deploy", "bench.yaml")
	args := []string{"-config", config}
	if !live {
		args = append(args, "-offline")
	}
	exited := make(chan int, 1)
	go func() { exited <- runServe(args) }()
	cfg, err := loadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	c := &e2eClient{t: t, base: "http://" + cfg.Server.Listen, http: &http.Client{Timeout: 60 * time.Second}}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case code := <-exited:
			t.Fatalf("serve exited with %d before it was ready", code)
		default:
		}
		if resp, err := c.http.Get(c.base + "/healthz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return c
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("serve not ready after 60s")
	return nil
}

func e2eSnapshot(t *testing.T, c *e2eClient, note string) int {
	t.Helper()
	out := c.must("POST", "/agents/"+e2eAgent+"/versions", map[string]any{"note": note}, 200, 201)
	if v := num(out["version"]); v > 0 {
		return int(v)
	}
	return e2eLatestVersion(t, c)
}

func e2eLatestVersion(t *testing.T, c *e2eClient) int {
	t.Helper()
	out := c.must("GET", "/agents/"+e2eAgent+"/versions", nil, 200)
	if out["agent"] != e2eAgent {
		t.Errorf("versions agent = %v", out["agent"])
	}
	latest := 0
	vs, _ := out["versions"].([]any)
	for _, vv := range vs {
		v, _ := vv.(map[string]any)
		for _, k := range []string{"version", "hash", "created_at"} {
			if _, ok := v[k]; !ok {
				t.Errorf("version row has no %q: %v", k, v)
			}
		}
		if n := int(num(v["version"])); n > latest {
			latest = n
		}
	}
	return latest
}

// e2eWait polls the evaluation until it leaves queued/running.
func e2eWait(t *testing.T, c *e2eClient, id string) map[string]any {
	t.Helper()
	timeout := 30 * time.Minute
	if s := os.Getenv("BENCH_E2E_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			timeout = d
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		ev := c.must("GET", "/evaluations/"+id, nil, 200)
		switch ev["status"] {
		case "done", "failed", "cancelled":
			return ev
		case "queued", "running":
		default:
			t.Fatalf("evaluation %s has status %v, not in the contract", id, ev["status"])
		}
		if time.Now().After(deadline) {
			t.Fatalf("evaluation %s still %v after %s", id, ev["status"], timeout)
		}
		time.Sleep(3 * time.Second)
	}
}

// e2eArmMetrics checks the per-arm metric objects and answers each arm's score.
func e2eArmMetrics(t *testing.T, where string, v any) map[string]float64 {
	t.Helper()
	arms, _ := v.(map[string]any)
	scores := map[string]float64{}
	for _, label := range []string{"A", "B"} {
		m, _ := arms[label].(map[string]any)
		if m == nil {
			t.Errorf("%s: no metrics for arm %s: %v", where, label, v)
			continue
		}
		for _, k := range []string{"score", "ci_low", "ci_high", "runs", "cost_usd", "tokens", "steps", "tool_calls", "wall_s"} {
			if _, ok := m[k]; !ok {
				t.Errorf("%s arm %s: no %q", where, label, k)
			}
		}
		s := num(m["score"])
		if s < 0 || s > 1 || num(m["ci_low"]) > s || num(m["ci_high"]) < s {
			t.Errorf("%s arm %s: score %v outside [ci_low %v, ci_high %v] or [0,1]", where, label, m["score"], m["ci_low"], m["ci_high"])
		}
		if num(m["runs"]) <= 0 {
			t.Errorf("%s arm %s: runs %v", where, label, m["runs"])
		}
		scores[label] = s
	}
	return scores
}

// e2eByKey indexes a JSON array of objects by one string field.
func e2eByKey(t *testing.T, v any, key string) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	arr, ok := v.([]any)
	if !ok {
		t.Errorf("want a JSON array, got %T: %v", v, v)
		return out
	}
	for _, e := range arr {
		m, _ := e.(map[string]any)
		if s, ok := m[key].(string); ok {
			out[s] = m
		}
	}
	return out
}

// firstArray answers the first array field of an object (responses are
// objects with a "mode" field, so lists come wrapped).
func firstArray(m map[string]any) []any {
	for _, k := range []string{"rows", "agents", "leaderboard", "items"} {
		if a, ok := m[k].([]any); ok {
			return a
		}
	}
	for _, v := range m {
		if a, ok := v.([]any); ok {
			return a
		}
	}
	return nil
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func writeE2E(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// e2eCopy copies a file or a directory tree.
func e2eCopy(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode().Perm())
	})
}
