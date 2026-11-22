package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schedule"
)

// X6 live scoring: the CLI, the scheduled job and the serve source over a
// project whose gateway is a local fake of Bifrost's log API.

type fakeLogs struct {
	mu      sync.Mutex
	methods []string
}

func (f *fakeLogs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.methods = append(f.methods, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	if r.Header.Get("X-Bifrost-Setup-Token") != "live-cli-token" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	_, _ = w.Write([]byte(`{"logs":[]}`))
}

func liveProject(t *testing.T) (config string, logs *fakeLogs) {
	t.Helper()
	logs = &fakeLogs{}
	srv := httptest.NewServer(logs)
	t.Cleanup(srv.Close)
	root := t.TempDir()
	cfg := "runs_root: runs\nbenchmarks_root: benchmarks\nserver:\n  listen: 127.0.0.1:1\n  mode: live\ngateway:\n  url: " + srv.URL +
		"\n  admin_token_env: LIVE_CLI_TOKEN\n"
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deploy", "bench.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVE_CLI_TOKEN", "live-cli-token")
	return filepath.Join(root, "deploy", "bench.yaml"), logs
}

func TestLivescoreCLI(t *testing.T) {
	config, logs := liveProject(t)
	ctx := context.Background()
	var out bytes.Buffer
	// The bare template has EDIT ME keys: it is written, and refused at pull.
	if code := livescoreInit(ctx, []string{"-config", config}, &out); code != exitOK {
		t.Fatalf("init = %d %s", code, out.String())
	}
	if code := livescorePull(ctx, []string{"-config", config}, &out); code == exitOK {
		t.Fatal("a pull with the template's EDIT ME keys passed")
	}
	if code := livescoreInit(ctx, []string{"-config", config, "-baseline-key", "vk-a", "-candidate-key", "vk-b"}, &out); code == exitOK {
		t.Fatal("init overwrote an existing config without -force")
	}
	out.Reset()
	if code := livescoreInit(ctx, []string{"-config", config, "-baseline-key", "vk-a", "-candidate-key", "vk-b", "-force"}, &out); code != exitOK {
		t.Fatalf("init -force = %d %s", code, out.String())
	}
	out.Reset()
	if code := livescorePull(ctx, []string{"-config", config, "-format", "json"}, &out); code != exitOK {
		t.Fatalf("pull = %d %s", code, out.String())
	}
	var res livescore.PullResult
	if err := json.Unmarshal(out.Bytes(), &res); err != nil || res.Rows["baseline"] != 0 {
		t.Fatalf("pull output %s (%v)", out.String(), err)
	}
	for _, m := range logs.methods {
		if !strings.HasPrefix(m, "GET /api/logs") {
			t.Errorf("the CLI sent %s", m)
		}
	}
	out.Reset()
	if code := livescoreStatus(ctx, []string{"-config", config, "-format", "json"}, &out); code != exitOK {
		t.Fatalf("status = %d", code)
	}
	var st livescore.Status
	if err := json.Unmarshal(out.Bytes(), &st); err != nil || st.Evidence != "live" || len(st.Versions) != 2 || st.Source.TokenEnv != "LIVE_CLI_TOKEN" {
		t.Fatalf("status %s (%v)", out.String(), err)
	}
	if strings.Contains(out.String(), "live-cli-token") {
		t.Error("status printed the credential")
	}
	out.Reset()
	if code := livescoreCompare(ctx, []string{"-config", config, "-fail-on", "worse"}, &out); code != exitOK || !strings.Contains(out.String(), "not_yet_known") {
		t.Fatalf("compare = %d %s", code, out.String())
	}
	out.Reset()
	if code := livescoreCompare(ctx, []string{"-simulate", "20", "-sessions", "60"}, &out); code != exitOK || !strings.Contains(out.String(), "20 synthetic streams") {
		t.Fatalf("simulate = %d %s", code, out.String())
	}
	if code := livescoreCompare(ctx, []string{"-config", config, "-fail-on", "better"}, &out); code != exitUsage {
		t.Errorf("-fail-on better = %d, want usage", code)
	}
}

func TestLivescoreScheduleJobAndServe(t *testing.T) {
	config, _ := liveProject(t)
	ctx := context.Background()
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Not configured: no job, and the page says it is not set up (N131: not a problem).
	file := &schedule.File{}
	addLivescoreSchedule(a, file)
	if len(file.Schedules) != 0 {
		t.Fatalf("schedules %+v without a config", file.Schedules)
	}
	st, err := serveLivescore(a, false).Status(ctx)
	if err != nil || st.Configured || st.Problem != "" {
		t.Fatalf("unconfigured status %+v %v", st, err)
	}
	text, err := livescore.Template(livescore.InitOptions{BaselineKey: "vk-a", CandidateKey: "vk-b"})
	if err != nil {
		t.Fatal(err)
	}
	text = strings.Replace(text, `# schedule: "*/15 * * * *"`, `schedule: "*/15 * * * *"`, 1)
	dir := filepath.Join(a.cfg.Root, livescore.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, livescore.ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	addLivescoreSchedule(a, file)
	s, ok := file.Find(schedule.JobLivescorePull)
	if !ok || s.Cron != "*/15 * * * *" {
		t.Fatalf("schedules %+v", file.Schedules)
	}
	if err := file.Validate(a.cfg.Root, loadExperiment); err != nil {
		t.Fatalf("validate: %v", err)
	}
	r, err := (scheduleRunner{a: a}).Run(ctx, s)
	if err != nil || !strings.HasPrefix(r.Summary, "live pull: 0 sessions scored") {
		t.Fatalf("run = %+v %v", r, err)
	}
	if c, err := serveLivescore(a, false).Compare(ctx); err != nil || c == nil || c.Result != livescore.ResultUnknown {
		t.Fatalf("compare %+v %v", c, err)
	}
	// 8-C-02: serve -offline keeps the stored views but refuses a pull, which
	// would read the gateway and could call a paid judge.
	off := serveLivescore(a, true)
	if res, err := off.Pull(ctx); !errors.Is(err, livescore.ErrOffline) || res != nil {
		t.Fatalf("offline pull = %+v %v, want ErrOffline", res, err)
	}
	if st, err := off.Status(ctx); err != nil || st == nil {
		t.Fatalf("offline status %+v %v", st, err)
	}
}

// TestLiveStatusUnconfiguredHasNoProblem checks that a project without
// livescore/config.yaml answers "not configured" with no problem text, so the
// page neither shows the CLI error nor treats a settings file as present, and
// that a file that does not load is a problem in plain words (N131).
func TestLiveStatusUnconfiguredHasNoProblem(t *testing.T) {
	config, _ := liveProject(t)
	ctx := context.Background()
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := serveLivescore(a, false).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || st.Problem != "" {
		t.Fatalf("unconfigured status: configured=%v problem=%q, want false and empty", st.Configured, st.Problem)
	}
	body, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "livescore init") || strings.Contains(string(body), `"problem"`) {
		t.Errorf("unconfigured status JSON %s names the CLI or a problem", body)
	}
	dir := filepath.Join(a.cfg.Root, livescore.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, livescore.ConfigFile), []byte("not: [valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = serveLivescore(a, false).Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Configured || !strings.HasPrefix(st.Problem, "the live scoring settings file could not be read: ") {
		t.Fatalf("broken config status: configured=%v problem=%q", st.Configured, st.Problem)
	}
}
