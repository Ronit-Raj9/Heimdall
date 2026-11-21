package main

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
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
