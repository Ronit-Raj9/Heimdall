package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/replayrec"
	"github.com/maximhq/bifrost/bench/shadowreplay"
)

// replayFixture is an org project with a Bifrost source pointing at a fake
// log server holding one session of one call.
func replayFixture(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	root := t.TempDir()
	var hits atomic.Int32
	now := time.Now().UTC().Add(-time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/api/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": []map[string]any{{"id": "r1", "timestamp": now, "status": "success", "session_id": "s1"}}})
		case "/api/logs/r1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "r1", "timestamp": now, "status": "success", "session_id": "s1", "provider": "openai", "model": "gpt-4o",
				"raw_request":  `{"model":"openai/gpt-4o","messages":[{"role":"user","content":"Is order 7 shipped?"}]}`,
				"raw_response": `{"choices":[{"message":{"role":"assistant","content":"Yes, it shipped."},"finish_reason":"stop"}]}`})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	if err := os.MkdirAll(filepath.Join(root, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := "sources:\n  - name: prod\n    kind: bifrost\n    url: " + srv.URL + "\n    filters:\n      errors: true\n"
	if err := os.WriteFile(filepath.Join(root, "inbox", "sources.yaml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, &hits
}

const replayJobParams = `{"source":"prod","since":"24h","candidate":{"provider":"openai","model":"gpt-4o-mini","budget_usd":1,"yes":true}}`

func TestReplayRefusedWhenOff(t *testing.T) {
	root, hits := replayFixture(t)
	env := jobEnv{ProjectRoot: root}
	var opened atomic.Int32
	old := openCaller
	openCaller = func(context.Context, jobEnv, *jobs.Job, modelParams) (investigator.Caller, func(), error) {
		opened.Add(1)
		return &investigator.FakeModel{Cap: 1}, func() {}, nil
	}
	t.Cleanup(func() { openCaller = old })
	_, _, err := validateReplayRun(context.Background(), env, json.RawMessage(replayJobParams))
	if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), shadowreplay.OffText) {
		t.Fatalf("validate = %v", err)
	}
	_, err = runReplayRun(context.Background(), env, &jobs.Job{ID: "j1", Params: json.RawMessage(replayJobParams)}, &lineLog{}, nil)
	if err == nil || !strings.Contains(err.Error(), shadowreplay.OffText) {
		t.Fatalf("run = %v", err)
	}
	if hits.Load() != 0 || opened.Load() != 0 {
		t.Fatalf("logs read %d times, model opened %d times while off", hits.Load(), opened.Load())
	}
}

func TestReplayRunWritesReport(t *testing.T) {
	root, _ := replayFixture(t)
	if _, err := shadowreplay.SaveSettings(root, shadowreplay.Settings{Enabled: true, MaxSessionsPerRun: 5, MaxUSDPerRun: 3}, "admin@example.com", time.Now()); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{ProjectRoot: root}
	fake := &investigator.FakeModel{Replies: []string{`{"choices":[{"message":{"role":"assistant","content":"Yes,  it shipped."},"finish_reason":"stop"}]}`}, CostUSD: 0.01, Cap: 1}
	old := openCaller
	openCaller = func(_ context.Context, _ jobEnv, _ *jobs.Job, p modelParams) (investigator.Caller, func(), error) {
		if p.Model != "gpt-4o-mini" || p.BudgetUSD != 1 || !p.Yes {
			t.Errorf("model params = %+v", p)
		}
		return fake, func() {}, nil
	}
	t.Cleanup(func() { openCaller = old })
	if _, _, err := validateReplayRun(context.Background(), env, json.RawMessage(`{"source":"prod","candidate":{"provider":"openai","model":"m","budget_usd":5,"yes":true}}`)); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("a cap over the org limit was accepted: %v", err)
	}
	if _, _, err := validateReplayRun(context.Background(), env, json.RawMessage(`{"source":"prod","candidate":{"provider":"openai","model":"m","budget_usd":1}}`)); err == nil || !strings.Contains(err.Error(), "Confirm the cost first.") {
		t.Fatalf("an unconfirmed cost was accepted: %v", err)
	}
	res, err := runReplayRun(context.Background(), env, &jobs.Job{ID: "j1", Params: json.RawMessage(replayJobParams)}, &lineLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["replay"] != "j1" {
		t.Fatalf("result = %v", res)
	}
	rep, err := shadowreplay.LoadReport(root, "j1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sessions) != 1 || rep.Sessions[0].Final != shadowreplay.StateEquivalent || rep.Note != shadowreplay.Note {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := shadowreplay.LoadRecording(root, replayrec.RecordingID("prod", "s1")); err != nil {
		t.Fatalf("recording not stored: %v", err)
	}
}
