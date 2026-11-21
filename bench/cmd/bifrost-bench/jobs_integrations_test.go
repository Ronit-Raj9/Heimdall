package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/webhooks"
)

func TestWebhookEventForJobs(t *testing.T) {
	j := jobs.Job{ID: "j1", OrgID: "o1", Kind: kindExperimentRun, Params: json.RawMessage(`{"exp_id":"e1"}`)}
	ev, ok := webhookEventFor(j, jobs.StatusSucceeded, []byte(`{"done":3}`), "")
	d, _ := ev.Data.(experimentFinishedData)
	if !ok || ev.Type != webhooks.EventExperimentFinished || d.ExpID != "e1" || string(d.Result) != `{"done":3}` {
		t.Fatalf("experiment event %+v", ev)
	}
	if _, ok := webhookEventFor(jobs.Job{Kind: "suite.lint"}, jobs.StatusSucceeded, nil, ""); ok {
		t.Fatal("succeeded non-experiment job fired")
	}
	if ev, ok := webhookEventFor(jobs.Job{Kind: "suite.lint"}, jobs.StatusFailed, nil, "boom"); !ok || ev.Type != webhooks.EventJobFailed {
		t.Fatal("failed job did not fire job.failed")
	}
}

func TestIntegrationsExportParams(t *testing.T) {
	for _, p := range []integrationsExportParams{{Format: "pdf", ExpID: "e"}, {Format: "otlp"}, {Format: "otlp", ExpID: "../x"}, {Format: "harbor", ExpID: "e"}, {Format: "csv", ExpID: "e", Suite: "s"}} {
		if _, err := p.validate(); err == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	if tgt, err := (integrationsExportParams{Format: "harbor", Suite: "s1"}).validate(); err != nil || tgt != "suite:s1" {
		t.Fatalf("harbor %q %v", tgt, err)
	}
}

// Gap G13: schedules run their experiment inside the schedule.fire job, so
// the job's record fires experiment.finished (not only job.failed).
func TestWebhookEventForScheduleFire(t *testing.T) {
	rec, _ := json.Marshal(schedule.Record{Schedule: "nightly", Status: "ok", Result: &schedule.Result{ExpID: "e_sched", Decision: "IMPROVED"}})
	j := jobs.Job{ID: "j2", OrgID: "o1", Kind: kindScheduleFire}
	ev, ok := webhookEventFor(j, jobs.StatusSucceeded, rec, "")
	d, _ := ev.Data.(experimentFinishedData)
	if !ok || ev.Type != webhooks.EventExperimentFinished || d.ExpID != "e_sched" || d.JobID != "j2" {
		t.Fatalf("schedule event %+v", ev)
	}
	ev, ok = webhookEventFor(j, jobs.StatusFailed, rec, "verdict failed")
	if d, _ := ev.Data.(experimentFinishedData); !ok || d.Status != "failed" || d.Error != "verdict failed" {
		t.Fatalf("failed schedule event %+v", ev)
	}
	// A schedule refused before any experiment ran is a failed job.
	refused, _ := json.Marshal(schedule.Record{Schedule: "nightly", Status: "refused"})
	if ev, ok := webhookEventFor(j, jobs.StatusFailed, refused, "over budget"); !ok || ev.Type != webhooks.EventJobFailed {
		t.Fatalf("refused schedule event %+v", ev)
	}
}

// Gap G13: an experiment the launcher runs in process (no job runner)
// still reaches the org's experiment.finished webhooks.
func TestNotifyInProcessFinishedDelivers(t *testing.T) {
	got := make(chan []byte, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	root := t.TempDir()
	url := srv.URL
	if _, err := webhooks.Open(root).Create(webhooks.Input{URL: &url}, "test", true); err != nil {
		t.Fatal(err)
	}
	webhookOrgsMu.Lock()
	webhookOrgs["org_g13_inproc"] = webhookOrg{root: root}
	webhookOrgsMu.Unlock()
	t.Cleanup(func() { webhookOrgsMu.Lock(); delete(webhookOrgs, "org_g13_inproc"); webhookOrgsMu.Unlock() })
	notifyInProcessFinished("org_g13_inproc", "e_quick", map[string]int{"runs": 2}, nil)
	select {
	case b := <-got:
		var ev struct {
			Type string `json:"type"`
			Data struct {
				ExpID  string `json:"exp_id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if err := json.Unmarshal(b, &ev); err != nil || ev.Type != webhooks.EventExperimentFinished || ev.Data.ExpID != "e_quick" || ev.Data.Status != "succeeded" {
			t.Fatalf("delivered %s (%v)", b, err)
		}
	default:
		t.Fatal("no webhook delivered")
	}
}
