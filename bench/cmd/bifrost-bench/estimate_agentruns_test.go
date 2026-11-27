package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/forecast"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// addTokenExperiment seals two runs of codex on m-a on suite other@1, each
// with 1000 tokens, so the agent has a typical token count on another suite.
func addTokenExperiment(t *testing.T, st *store.FSStore) {
	t.Helper()
	ctx := context.Background()
	m := &schemas.Manifest{ExpID: "e_other", SuiteID: "other", SuiteVersion: "1", CreatedAt: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC),
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: "clean", Metric: "verified_success"}},
		Arms: map[string]schemas.ArmManifest{"A": {Label: "m-a", ConfigHash: "hO",
			Config: schemas.AgentConfig{ID: "codex", Model: "m-a", Adapter: "command", Gateway: schemas.GatewayPolicy{Provider: "openai"}}}}}
	for i := 1; i <= 2; i++ {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_a_t%d", i), PairID: fmt.Sprintf("p_t%d", i),
			Task: fmt.Sprintf("t%d", i), Arm: "A", Condition: "clean", Trial: 1, OrderIndex: i - 1, Counted: true})
	}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, sl := range m.Slots {
		sl.ExpID = m.ExpID
		r := &schemas.Run{RunID: "r_other_" + sl.SlotID[2:], Attempt: 1, Stage: schemas.StageCleanup, Outcome: schemas.OutcomePass}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		r.ExpID, r.SlotID, r.PairID = m.ExpID, sl.SlotID, sl.PairID
		metrics := `{"verified_success":{"state":"observed","v":1,"evidence":"outcome"},"tokens":{"state":"observed","v":1000,"evidence":"gateway"},"wall_time_s":{"state":"observed","v":60,"evidence":"outcome"}}`
		if err := st.WriteRunFile(ctx, r, "metrics.json", []byte(metrics)); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteRunFile(ctx, r, "proxy.ledger.jsonl", []byte(`{"kind":"message","action":"pass","upstream":"real","status":200}`+"\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

func plannedOn(suite string) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: "e_new", SuiteID: suite, SuiteVersion: "1",
		Arms: map[string]schemas.ArmManifest{"A": {Label: "A", ConfigHash: "hA", Config: schemas.AgentConfig{ID: "codex", Model: "m-a"}}}}
	for task := 1; task <= 4; task++ {
		m.Slots = append(m.Slots, schemas.Slot{Arm: "A", Task: fmt.Sprintf("t%d", task), Counted: true})
	}
	return m
}

// TestForecastListPriceWithoutHistory: an arm with no history and no
// library run on its suite gets the rough list-price estimate from its
// agent's tokens on another suite (basis agent_tokens, which the evaluation
// estimate reports as estimate_basis "list_price"); with history on its
// suite nothing changes.
func TestForecastListPriceWithoutHistory(t *testing.T) {
	st := forecastStore(t)
	addTokenExperiment(t, st)
	a := &app{store: st, prices: testPrices(t), logger: schemas.NoopLogger{}}

	f, err := a.forecast(context.Background(), plannedOn("fresh"))
	if err != nil {
		t.Fatal(err)
	}
	arm := f.Arms[0]
	if string(arm.Basis) != forecast.BasisAgentTokens || !strings.HasPrefix(arm.BasisText, "rough:") || arm.CostUSD.Median == nil {
		t.Fatalf("no history: arm %+v", arm)
	}
	// 4 runs x 1000 tokens x $1 per million tokens.
	if got := *arm.CostUSD.Median; got < 0.0039 || got > 0.0041 {
		t.Fatalf("median cost %v, want about 0.004", got)
	}

	f, err = a.forecast(context.Background(), plannedOn("s"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Arms[0].Basis != schemas.BasisConfigHash {
		t.Fatalf("with history: basis %s, want config_hash", f.Arms[0].Basis)
	}
}

func TestTokenPrice(t *testing.T) {
	a := &app{prices: testPrices(t)}
	lo, mid, hi, ok := a.tokenPrice("m-a")
	if !ok || lo != 1e-6 || mid != 1e-6 || hi != 1e-6 {
		t.Fatalf("m-a %v %v %v %v", lo, mid, hi, ok)
	}
	if _, _, _, ok := a.tokenPrice("unknown"); ok {
		t.Fatal("an unpriced model has no token price")
	}
}
