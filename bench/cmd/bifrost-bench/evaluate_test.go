package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mustSpec(t *testing.T, args ...string) *schemas.EvaluationSpec {
	t.Helper()
	t.Setenv(syncServerEnv, "")
	o, err := parseEvalFlags("run", args)
	if err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	s, err := buildEvalSpec(o)
	if err != nil {
		t.Fatalf("spec %v: %v", args, err)
	}
	return s
}

func TestEvalRunFlagsScore(t *testing.T) {
	s := mustSpec(t, "--agent", "codex@v2", "--model", "gpt-5", "--pack", "coding", "--faults", "model-down,broken-tool")
	if s.Mode != schemas.EvaluationModeScore || len(s.Arms) != 1 {
		t.Fatalf("mode %q arms %d", s.Mode, len(s.Arms))
	}
	a := s.Arms[0]
	if a.Label != "A" || a.Agent != "codex" || a.Version != 2 || a.Model != "gpt-5" {
		t.Fatalf("arm %+v", a)
	}
	if s.TasksPerBenchmark != 8 || s.Trials != 2 || s.Pack != "coding" {
		t.Fatalf("defaults %+v", s)
	}
	if strings.Join(s.Faults, ",") != "clean,model-down,broken-tool" {
		t.Fatalf("faults %v", s.Faults)
	}
	if s.Name == "" {
		t.Fatal("no default name")
	}
}

func TestEvalRunFlagsAB(t *testing.T) {
	s := mustSpec(t, "--agent", "codex@v1", "--vs", "codex@v2", "--model", "m", "--benchmarks", "a, b", "--tasks", "3", "--trials", "1")
	if s.Mode != schemas.EvaluationModeVersions || len(s.Arms) != 2 || s.Arms[1].Version != 2 || s.Arms[1].Label != "B" {
		t.Fatalf("versions: %+v", s)
	}
	if strings.Join(s.Benchmarks, ",") != "a,b" || s.TasksPerBenchmark != 3 || s.Trials != 1 {
		t.Fatalf("benchmarks %+v", s)
	}
	s = mustSpec(t, "--agent", "mine", "--vs", "claude-code", "--model", "m", "--pack", "coding")
	if s.Mode != schemas.EvaluationModeOpponent || s.Arms[1].Agent != "claude-code" {
		t.Fatalf("opponent: %+v", s)
	}
	s = mustSpec(t, "--agent", "agents/mine.yaml@v3", "--vs-model", "m2", "--model", "m1", "--pack", "coding")
	if s.Mode != schemas.EvaluationModeModels || s.Arms[1].Model != "m2" || s.Arms[1].Agent != "mine" || s.Arms[1].Version != 3 {
		t.Fatalf("models: %+v", s)
	}
}

func TestEvalRunFlagsErrors(t *testing.T) {
	t.Setenv(syncServerEnv, "")
	bad := [][]string{
		{"--model", "m", "--pack", "coding"},
		{"--agent", "a", "--pack", "coding"},
		{"--agent", "a", "--model", "m"},
		{"--agent", "a", "--model", "m", "--pack", "coding", "--vs", "b", "--vs-model", "x"},
		{"--agent", "a@vx", "--model", "m", "--pack", "coding"},
		{"--agent", "a", "--model", "m", "--pack", "coding", "--faults", "earthquake"},
		{"--agent", "a@v1", "--vs", "a@v1", "--model", "m", "--pack", "coding"},
		{"--agent", "a", "--model", "m", "--vs-model", "m", "--pack", "coding"},
		{"--agent", "a", "--model", "m", "--pack", "coding", "--tasks", "0"},
	}
	for _, args := range bad {
		o, err := parseEvalFlags("run", args)
		if err == nil {
			_, err = buildEvalSpec(o)
		}
		if err == nil {
			t.Errorf("%v: want error", args)
		}
	}
	if _, err := parseEvalFlags("show", nil); err == nil {
		t.Error("show without id: want error")
	}
	if _, err := parseEvalFlags("list", []string{"x"}); err == nil {
		t.Error("list with arg: want error")
	}
	o, err := parseEvalFlags("report", []string{"ev_1", "--json"})
	if err != nil || !o.asJSON || o.pos[0] != "ev_1" {
		t.Fatalf("report flags: %+v %v", o, err)
	}
}

const sampleReport = `{"evaluation":"ev_1","arms":[{"label":"A","agent":"codex","version":2,"model":"gpt-x"},{"label":"B","agent":"codex","version":3,"model":"gpt-x"}],
 "capabilities":[{"capability":"terminal","benchmarks":["hd-harbor-tb2"],
   "arms":{"A":{"score":0.41,"ci_low":0.30,"ci_high":0.52,"runs":30,"cost_usd":3.2},"B":{"score":0.49,"ci_low":0.38,"ci_high":0.60,"runs":30,"cost_usd":3.5}}}],
 "benchmarks":[{"benchmark":"hd-harbor-tb2","capability":"terminal","experiment_id":"exp_1","arms":{"A":{"score":0.41,"runs":30,"cost_usd":3.2},"B":{"score":0.49,"runs":30,"cost_usd":3.5}},"verdict":{"winner":"B","delta":0.08,"significant":true,"summary":"B solves 8 points more"}}],
 "reliability":[{"fault":"model-down","arms":{"A":{"score":0.2,"drop":-0.21}}}],
 "safety":{"attack_resisted":{"A":0.88},"cheat_rate":{"A":0.05}},
 "overall":{"winner":"B","wins":{"A":0,"B":1,"tie":0},"summary":"B is better on 1 of 1 benchmarks"}}`

func TestRenderReportTable(t *testing.T) {
	var r evalReport
	if err := json.Unmarshal([]byte(sampleReport), &r); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	renderReport(&buf, &r)
	got := buf.String()
	for _, want := range []string{
		"report card ev_1",
		"A = codex@v2 gpt-x",
		"CAPABILITY", "terminal", "41% [30-52] n=30", "49% [38-60] n=30",
		"B solves 8 points more",
		"model-down", "20% (-21)",
		"attacks resisted 88%", "cheat rate 5%",
		"overall: B is better on 1 of 1 benchmarks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// Columns line up: header and the terminal row start the A column at the same offset.
	lines := strings.Split(got, "\n")
	var head, row string
	for _, l := range lines {
		if strings.HasPrefix(l, "CAPABILITY") {
			head = l
		}
		if strings.HasPrefix(l, "terminal") {
			row = l
		}
	}
	if head == "" || row == "" || strings.Index(head, "A ") != strings.Index(row, "41%") {
		t.Errorf("columns not aligned:\n%q\n%q", head, row)
	}
}

func TestRenderEvalListAndShow(t *testing.T) {
	ev := schemas.Evaluation{ID: "ev_1", Status: "running", CreatedAt: time.Unix(0, 0),
		Spec:     schemas.EvaluationSpec{Name: "n", Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}, TasksPerBenchmark: 5, Trials: 2, Faults: []string{"clean"}},
		Children: []schemas.EvaluationChild{{Benchmark: "b1", ExperimentID: "exp_1", Status: "running"}, {Benchmark: "b2", Status: "skipped", Skipped: "needs desktop"}}}
	var buf bytes.Buffer
	renderEvalList(&buf, []schemas.Evaluation{ev})
	if !strings.Contains(buf.String(), "ev_1") || !strings.Contains(buf.String(), "running") {
		t.Fatalf("list: %s", buf.String())
	}
	buf.Reset()
	renderEvaluation(&buf, &ev)
	for _, want := range []string{"arm A:    codex m", "exp_1", "needs desktop"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("show missing %q:\n%s", want, buf.String())
		}
	}
}

func TestEvalLocalRoundTrip(t *testing.T) {
	root := t.TempDir()
	t.Setenv(syncServerEnv, "")
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "deploy", "bench.yaml")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if rc := runEvalSub("run", []string{"--agent", "codex", "--model", "m", "--pack", "coding", "--json", "-config", cfg}, &out); rc != exitOK {
		t.Fatalf("run rc %d", rc)
	}
	var ev schemas.Evaluation
	if err := json.Unmarshal(out.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Children) != 4 || ev.Status != "queued" {
		t.Fatalf("children %+v", ev)
	}
	out.Reset()
	if rc := runEvalSub("cancel", []string{ev.ID, "-config", cfg}, &out); rc != exitOK || !strings.Contains(out.String(), "cancelled") {
		t.Fatalf("cancel rc %d %s", rc, out.String())
	}
	out.Reset()
	if rc := runEvalSub("list", []string{"-config", cfg}, &out); rc != exitOK || !strings.Contains(out.String(), ev.ID) {
		t.Fatalf("list rc %d %s", rc, out.String())
	}
	out.Reset()
	if rc := runEvalSub("run", []string{"--agent", "codex", "--model", "m", "--benchmarks", "x,y", "--faults", "model-down", "--estimate", "--json", "-config", cfg}, &out); rc != exitOK {
		t.Fatalf("estimate rc %d", rc)
	}
	var est evalEstimate
	_ = json.Unmarshal(out.Bytes(), &est)
	if est.Runs != 2*8*2*1*2 { // 2 benchmarks, 8 tasks (default), 2 trials, 1 arm, clean + model-down
		t.Fatalf("runs %d", est.Runs)
	}
}

func TestEvalHTTPBackend(t *testing.T) {
	var gotSpec schemas.EvaluationSpec
	var gotAuth, cancelled string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/bench/evaluations":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotSpec)
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(schemas.Evaluation{ID: "ev_9", Spec: gotSpec, Status: "queued"})
		case r.Method == "GET" && r.URL.Path == "/api/bench/evaluations/ev_9/report":
			_, _ = io.WriteString(w, sampleReport)
		case r.Method == "POST" && r.URL.Path == "/api/bench/evaluations/ev_9/cancel":
			cancelled = "yes"
			_ = json.NewEncoder(w).Encode(schemas.Evaluation{ID: "ev_9", Status: "cancelled"})
		case r.Method == "GET" && r.URL.Path == "/api/bench/agents/codex/versions":
			_, _ = io.WriteString(w, `{"agent":"codex","versions":[{"version":1,"hash":"abc","note":"first"}]}`)
		default:
			http.Error(w, `{"error":{"message":"nope"}}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	var out bytes.Buffer
	if rc := runEvalSub("run", []string{"--agent", "codex@v2", "--vs-model", "m2", "--model", "m1", "--pack", "coding", "-server", srv.URL, "-token", "hm_x"}, &out); rc != exitOK {
		t.Fatalf("run rc %d", rc)
	}
	if gotAuth != "Bearer hm_x" || gotSpec.Mode != "models" || len(gotSpec.Arms) != 2 || !strings.Contains(out.String(), "ev_9") {
		t.Fatalf("auth %q spec %+v out %s", gotAuth, gotSpec, out.String())
	}
	out.Reset()
	if rc := runEvalSub("report", []string{"ev_9", "-server", srv.URL}, &out); rc != exitOK || !strings.Contains(out.String(), "41% [30-52] n=30") {
		t.Fatalf("report rc %d %s", rc, out.String())
	}
	out.Reset()
	if rc := runEvalSub("cancel", []string{"ev_9", "-server", srv.URL}, &out); rc != exitOK || cancelled != "yes" {
		t.Fatalf("cancel rc %d", rc)
	}
	out.Reset()
	if rc := runAgentVersionsCmd("versions", []string{"codex", "-server", srv.URL}, &out); rc != exitOK || !strings.Contains(out.String(), "first") {
		t.Fatalf("versions rc %d %s", rc, out.String())
	}
	if rc := runEvalSub("show", []string{"ev_missing", "-server", srv.URL}, io.Discard); rc == exitOK {
		t.Fatal("show missing: want failure")
	}
}

func TestAgentSnapshotLocal(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "agents", "mine.yaml")
	_ = os.WriteFile(cfg, []byte("id: mine\n"), 0o644)
	now := time.Unix(100, 0)
	r1, made, err := localAgentSnapshot(root, "mine", "first", "me", now)
	if err != nil || !made || r1.Version != 1 {
		t.Fatalf("v1 %+v %v %v", r1, made, err)
	}
	r, made, _ := localAgentSnapshot(root, "mine", "", "me", now)
	if made || r.Version != 1 {
		t.Fatalf("same hash made a version: %+v", r)
	}
	_ = os.WriteFile(cfg, []byte("id: mine\nmodel: x\n"), 0o644)
	r2, made, _ := localAgentSnapshot(root, "mine", "second", "me", now)
	if !made || r2.Version != 2 {
		t.Fatalf("v2 %+v", r2)
	}
	if _, err := os.Stat(filepath.Join(root, ".heimdall", "agent-versions", "mine", "v2.yaml")); err != nil {
		t.Fatal(err)
	}
	vs, _ := localAgentVersions(root, "mine")
	var buf bytes.Buffer
	renderAgentVersions(&buf, vs)
	if !strings.Contains(buf.String(), "v2") || !strings.Contains(buf.String(), "second") {
		t.Fatalf("render: %s", buf.String())
	}
}
