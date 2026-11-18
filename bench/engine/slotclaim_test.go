package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestScriptedPlanRefusedFailsClosed (F1): another bench process claimed
// the run's key slot and bound its own scripted plan. This process's
// scripted plan is refused by the fault proxy, so the run fails closed
// before the agent starts, and the other process's plan is untouched.
func TestScriptedPlanRefusedFailsClosed(t *testing.T) {
	ctx := context.Background()
	srv, err := faultproxy.NewServer(faultproxy.Config{
		Upstream: faultproxy.UpstreamScripted, LedgerPath: filepath.Join(t.TempDir(), "ledger.jsonl"),
		Slots:  []faultproxy.SlotConfig{{Name: "fp-slot-1", Auth: "slot-auth-1"}},
		Getenv: func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctrl, data := httptest.NewServer(srv.ControlHandler()), httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ctrl.Close(); data.Close(); _ = srv.Close() })
	other := faultproxy.NewClient(ctrl.URL, faultproxy.WithToken(srv.ControlToken()))
	if _, err := other.ClaimSlots(ctx, faultproxy.ClaimRequest{Slots: []string{"fp-slot-1"}, RunID: "r_other"}); err != nil {
		t.Fatal(err)
	}
	if err := other.SetPlan(ctx, "fp-slot-1", schemas.FaultPlan{Mode: faultproxy.ModeScripted, RunID: "r_other"}); err != nil {
		t.Fatal(err)
	}

	b, m, slot := pipelineFixture(t, "clean")
	reg := *b.reg
	reg.Fault = faultproxy.NewClient(ctrl.URL, faultproxy.WithToken(srv.ControlToken())) // this process: another owner
	res, err := NewPipeline(&reg, b.root, nil, WithFaultMode(faultModeScripted)).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: slot, Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Outcome != schemas.OutcomeAbortedInfra {
		t.Fatalf("outcome %s, want %s", res.Run.Outcome, schemas.OutcomeAbortedInfra)
	}
	if b.w.count("exec") != 0 {
		t.Fatal("the agent ran on a slot whose scripted plan was refused")
	}
	if len(res.Run.Failures) == 0 || res.Run.Failures[0].Stage != schemas.StagePrepare ||
		!strings.Contains(res.Run.Failures[0].Error, "claimed by another bench process") {
		t.Fatalf("failures %+v", res.Run.Failures)
	}
	// The other process's run is still scripted: its call is answered by
	// the scripted model under its own run id.
	req, _ := http.NewRequest(http.MethodPost, data.URL+"/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-6","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "slot-auth-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	entries, err := other.Ledger(ctx, "r_other")
	if err != nil || len(entries) != 1 || entries[0].Upstream != faultproxy.UpstreamScripted {
		t.Fatalf("other process's ledger = %+v, %v", entries, err)
	}
}
