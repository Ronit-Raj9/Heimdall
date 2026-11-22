package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/mail"
	"github.com/maximhq/bifrost/bench/notify"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func TestScheduleFailedEvent(t *testing.T) {
	rec := schedule.Record{Schedule: "nightly", Status: schedule.StatusRefused, Reason: "daily cap: spent 9 USD"}
	b, _ := json.Marshal(rec)
	j := jobs.Job{ID: "job_1", Kind: kindScheduleFire, OrgID: "org1", Params: json.RawMessage(`{"name":"nightly"}`)}
	ev, ok := scheduleFailedEvent(j, jobs.StatusFailed, b, "schedule nightly refused")
	if !ok || ev.Type != webhooks.EventScheduleFailed || ev.Summary == nil || ev.Summary.Title != "Scheduled check nightly did not start" || ev.Summary.Text != rec.Reason {
		t.Fatalf("refused: %+v %+v", ev, ev.Summary)
	}
	// A run that failed its check.
	b, _ = json.Marshal(schedule.Record{Schedule: "nightly", Status: schedule.StatusFailed, Result: &schedule.Result{ExpID: "e1", Summary: "regressed"}})
	if ev, ok := scheduleFailedEvent(j, jobs.StatusSucceeded, b, ""); !ok || ev.Data.(scheduleFailedData).ExpID != "e1" {
		t.Fatalf("failed check: %+v %v", ev, ok)
	}
	// Skipped (still running) and OK are not failures; other kinds never.
	b, _ = json.Marshal(schedule.Record{Schedule: "nightly", Status: schedule.StatusSkipped})
	if _, ok := scheduleFailedEvent(j, jobs.StatusFailed, b, "busy"); ok {
		t.Fatal("skipped alerted")
	}
	b, _ = json.Marshal(schedule.Record{Schedule: "nightly", Status: schedule.StatusOK})
	if _, ok := scheduleFailedEvent(j, jobs.StatusSucceeded, b, ""); ok {
		t.Fatal("ok alerted")
	}
	// A job that failed before writing a record names the schedule from its params.
	if ev, ok := scheduleFailedEvent(j, jobs.StatusFailed, nil, "no organization app"); !ok || ev.Data.(scheduleFailedData).Schedule != "nightly" {
		t.Fatalf("no record: %+v", ev)
	}
	if _, ok := scheduleFailedEvent(jobs.Job{Kind: kindExperimentRun}, jobs.StatusFailed, nil, "x"); ok {
		t.Fatal("experiment.run mapped to schedule.failed")
	}
}

func TestOfflineTracker(t *testing.T) {
	tr := &offlineTracker{}
	if got := tr.changes("o", []schemas.RunnerView{{ID: "r1", State: schemas.RunnerLost}, {ID: "r2", State: "idle"}}); len(got) != 0 {
		t.Fatalf("first sight alerted %v", got)
	}
	if got := tr.changes("o", []schemas.RunnerView{{ID: "r1", State: schemas.RunnerLost}, {ID: "r2", State: schemas.RunnerLost, Name: "gpu-1"}}); len(got) != 1 || got[0].ID != "r2" {
		t.Fatalf("drop %v", got)
	}
	if got := tr.changes("o", []schemas.RunnerView{{ID: "r2", State: schemas.RunnerLost}}); len(got) != 0 {
		t.Fatalf("still lost alerted again %v", got)
	}
	ev := workerOfflineEvent("o", schemas.RunnerView{ID: "r2", Name: "gpu-1", LastSeen: time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)})
	if ev.Type != webhooks.EventWorkerOffline || ev.Summary.Title != "Worker gpu-1 is offline" || !strings.Contains(ev.Summary.Text, "2026-11-22 03:00 UTC") {
		t.Fatalf("event %+v", ev.Summary)
	}
}

// TestEvaluationFinishedReachesSlackAndHistory runs the whole path: the
// notice goes to a Slack hook (Block Kit body), the digest history, and a
// scheduled evaluation's outcome is settled on its schedule.
func TestEvaluationFinishedReachesSlackAndHistory(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
	}))
	defer srv.Close()
	root := t.TempDir()
	if _, err := webhooks.Open(root).Create(webhooks.Input{URL: &srv.URL, Format: ptrTo(webhooks.FormatSlack)}, "u1", true); err != nil {
		t.Fatal(err)
	}
	notify.SetDefault(&notify.Hub{Mail: mail.NewLog(nil), PublicURL: "https://h.example.com"})
	t.Cleanup(func() { notify.SetDefault(nil) })
	score, change := 0.61, -0.06
	n := handlers.EvaluationNotice{EvaluationID: "ev_1", Status: schemas.EvaluationDone, Agent: "codex", Version: 7, Score: &score, Change: &change,
		Baseline: &handlers.NoticeBaseline{Source: handlers.BaselineFromPreviousRun, Score: 0.67, EvaluationID: "ev_0"}, Regressed: true,
		Regressions: []string{"6 points lower than the previous run"}, Link: "/evaluations/ev_1", FinishedAt: time.Now(), Schedule: "nightly"}
	env := handlers.OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeRecorded}
	onEvaluationFinished(env, &schemas.Evaluation{ID: "ev_1", Schedule: "nightly", CreatedAt: time.Now()}, n)

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("slack got %d messages, want finished and regressed", len(bodies))
	}
	reg := bodies[1]
	if !strings.HasPrefix(reg["text"].(string), "codex v7 regressed: 61% (-6 points)") {
		t.Fatalf("regressed text %v", reg["text"])
	}
	blocks, _ := json.Marshal(reg["blocks"])
	if !strings.Contains(string(blocks), "https://h.example.com/evaluations/ev_1") || !strings.Contains(string(blocks), "vs previous run (67%)") {
		t.Fatalf("blocks %s", blocks)
	}
	recs, err := notify.Open(root).History(time.Time{})
	if err != nil || len(recs) != 1 || !recs[0].Regressed || recs[0].Agent != "codex" {
		t.Fatalf("history %+v %v", recs, err)
	}
	// The schedule no longer exists here: nothing is settled, nothing breaks.
	if _, err := os.Stat(filepath.Join(handlers.ScheduleStateDir(root), schedule.RecordsFile)); err == nil {
		t.Fatal("settled a schedule that does not exist")
	}
	res := scheduledOutcome(n)
	if res.Decision != string(schemas.DecisionRegressed) || res.EvaluationID != "ev_1" || !strings.Contains(res.Summary, "score 61% (-6 points vs the previous run)") {
		t.Fatalf("outcome %+v", res)
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestSettleScheduledEvaluationRecordsOutcome(t *testing.T) {
	root := t.TempDir()
	evDir := filepath.Join(root, ".heimdall", "evaluations")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `{"id":"ev_20261120_000000_abc","spec":{"name":"n","mode":"score","arms":[{"label":"A","agent":"codex","model":"m"}],"benchmarks":["hd-a"],"tasks_per_benchmark":2,"trials":1,"faults":["clean"]},"status":"done","children":[],"created_at":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(evDir, "ev_20261120_000000_abc.json"), []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: nightly-codex, evaluation: ev_20261120_000000_abc, cron: \"0 2 * * *\", timezone: UTC, max_usd_per_day: 6}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	score, change := 0.61, -0.06
	n := handlers.EvaluationNotice{EvaluationID: "ev_20261122_020000_new", Status: schemas.EvaluationDone, Score: &score, Change: &change, Runs: 16,
		Baseline: &handlers.NoticeBaseline{Source: handlers.BaselineFromPreviousRun, Score: 0.67}, Regressed: true, Regressions: []string{"6 points lower"}}
	env := handlers.OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeRecorded, Services: handlers.Services{OrgID: "org1", Hosted: true}}
	ev := &schemas.Evaluation{ID: n.EvaluationID, Schedule: "nightly-codex", CreatedAt: time.Now()}
	if err := settleScheduledEvaluation(t.Context(), env, ev, n); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(handlers.ScheduleStateDir(root), schedule.RecordsFile))
	if err != nil {
		t.Fatal(err)
	}
	var rec schedule.Record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != schedule.StatusFailed || rec.Result == nil || rec.Result.Decision != "regressed" || rec.Result.EvaluationID != n.EvaluationID || rec.Schedule != "nightly-codex" {
		t.Fatalf("record %+v %+v", rec, rec.Result)
	}
}
