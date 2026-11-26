package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/webhooks"
)

// TestImprovementEventsDispatched: a finished and a paused run each send
// their event through the org's webhook outbox with the documented data; a
// run that is not finished sends no improvement.finished.
func TestImprovementEventsDispatched(t *testing.T) {
	var mu sync.Mutex
	var got []webhooks.Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var ev webhooks.Event
		_ = json.Unmarshal(b, &ev)
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	root := t.TempDir()
	url := srv.URL
	if _, err := webhookStore(auth.LocalOrgID, root, false).Create(webhooks.Input{URL: &url,
		Events: &[]string{webhooks.EventImprovementFinished, webhooks.EventImprovementPaused}}, "u1", true); err != nil {
		t.Fatal(err)
	}
	notify := improvementNotifier(&app{cfg: &lib.Config{Root: root}})
	rec := &improve.Record{ID: "imp_ev-1", Name: "ev", Status: improve.StatusDone, SpentUSD: 3.5,
		Spec:    improve.Spec{Agent: "agents/a.yaml", Suite: "s@1"},
		Verdict: &improve.Verdict{State: improve.VerdictSupported, Headline: "Version 1 is better.", Gain: 0.12}}
	notify(webhooks.EventImprovementFinished, rec)
	running := *rec
	running.Status = improve.StatusRunning
	notify(webhooks.EventImprovementFinished, &running)
	paused := *rec
	paused.Status, paused.Verdict = improve.StatusAwaiting, nil
	notify(webhooks.EventImprovementPaused, &paused)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || got[0].Type != webhooks.EventImprovementFinished || got[1].Type != webhooks.EventImprovementPaused {
		t.Fatalf("events %+v", got)
	}
	b, _ := json.Marshal(got[0].Data)
	var data map[string]any
	_ = json.Unmarshal(b, &data)
	for _, k := range []string{"improvement_id", "name", "agent", "suite", "status", "state", "headline", "gain_points", "spent_usd", "url"} {
		if _, ok := data[k]; !ok {
			t.Errorf("data misses %s: %s", k, b)
		}
	}
	if data["improvement_id"] != "imp_ev-1" || data["headline"] != "Version 1 is better." || data["gain_points"] != 12.0 {
		t.Errorf("data %s", b)
	}
}
