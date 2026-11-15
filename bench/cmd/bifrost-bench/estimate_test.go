package main

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// forecastStore is a synthetic run store on disk: e_past has sealed runs of
// config hA (codex on m-a) with costs and a real-model proxy ledger, and
// e_scripted has runs of the same config against the scripted model, which
// the forecast must leave out.
func forecastStore(t *testing.T) *store.FSStore {
	t.Helper()
	root := t.TempDir()
	t.Cleanup(func() { makeWritable(t, root) })
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	addSealedExperiment(t, st, "e_past", map[string]float64{"A": 0.10}, `{"kind":"message","action":"pass","upstream":"real","status":200}`)
	addSealedExperiment(t, st, "e_scripted", map[string]float64{"A": 9}, `{"kind":"message","action":"pass","upstream":"scripted","status":200}`)
	return st
}

// addSealedExperiment writes an experiment on suite s@1 with one arm per
// cost entry (arm A is config hA, codex on m-a; others hB.., codex on m-b..)
// over tasks t1 to t4, one trial, every run passing except arm A on t4, and
// seals every run with metrics and the given ledger line.
func addSealedExperiment(t *testing.T, st *store.FSStore, expID string, costs map[string]float64, ledger string) {
	t.Helper()
	ctx := context.Background()
	m := &schemas.Manifest{ExpID: expID, SuiteID: "s", SuiteVersion: "1", CreatedAt: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC),
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: "clean", Metric: "verified_success"}},
		Arms:       map[string]schemas.ArmManifest{}}
	for arm := range costs {
		model := "m-" + strings.ToLower(arm)
		m.Arms[arm] = schemas.ArmManifest{Label: model, ConfigHash: "h" + arm,
			Config: schemas.AgentConfig{ID: "codex", Model: model, Adapter: "command", Gateway: schemas.GatewayPolicy{Provider: "openai"}}}
	}
	i := 0
	for task := 1; task <= 4; task++ {
		for _, arm := range sortedArms(costs) {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%s_t%d", strings.ToLower(arm), task), PairID: fmt.Sprintf("p_t%d", task),
				Task: fmt.Sprintf("t%d", task), Arm: arm, Condition: "clean", Trial: 1, OrderIndex: i, Counted: true})
			i++
		}
	}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, sl := range m.Slots {
		sl.ExpID = m.ExpID
		y, outcome := 1, schemas.OutcomePass
		if sl.Arm == "A" && sl.Task == "t4" {
			y, outcome = 0, schemas.OutcomeFail
		}
		r := &schemas.Run{RunID: "r_" + expID[2:] + "_" + sl.SlotID[2:], Attempt: 1, Stage: schemas.StageCleanup, Outcome: outcome}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		r.ExpID, r.SlotID, r.PairID = m.ExpID, sl.SlotID, sl.PairID
		metrics := fmt.Sprintf(`{"verified_success":{"state":"observed","v":%d,"evidence":"outcome"},"cost_usd":{"state":"observed","v":%v,"evidence":"gateway"},"wall_time_s":{"state":"observed","v":60,"evidence":"outcome"}}`,
			y, costs[sl.Arm])
		if err := st.WriteRunFile(ctx, r, "metrics.json", []byte(metrics)); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteRunFile(ctx, r, "proxy.ledger.jsonl", []byte(ledger+"\n")); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}

func sortedArms(costs map[string]float64) []string {
	var out []string
	for _, a := range []string{"A", "B", "C", "D"} {
		if _, ok := costs[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

func testPrices(t *testing.T) *evidence.PriceTable {
	t.Helper()
	pt, err := evidence.ParsePrices([]byte(`{"id":"test","models":{"m-a":{"input_per_mtok":1,"output_per_mtok":1,"cache_read_per_mtok":1,"cache_write_per_mtok":1},
		"m-b":{"input_per_mtok":1,"output_per_mtok":1,"cache_read_per_mtok":1,"cache_write_per_mtok":1}}}`), "test")
	if err != nil {
		t.Fatal(err)
	}
	return pt
}

func TestForecastFromStore(t *testing.T) {
	a := &app{store: forecastStore(t), prices: testPrices(t), logger: schemas.NoopLogger{}}
	planned := &schemas.Manifest{ExpID: "e_new", SuiteID: "s", SuiteVersion: "1",
		Experiment: schemas.Experiment{Budget: schemas.BudgetSpec{TotalUSD: 2}},
		Arms: map[string]schemas.ArmManifest{
			"A": {Label: "same config", ConfigHash: "hA", Config: schemas.AgentConfig{ID: "codex", Model: "m-a"}},
			"B": {Label: "new model", ConfigHash: "hZ", Config: schemas.AgentConfig{ID: "codex", Model: "m-b", Budgets: schemas.Budgets{USD: 0.2, WallS: 900}}},
		}}
	for task := 1; task <= 4; task++ {
		for _, arm := range []string{"A", "B"} {
			planned.Slots = append(planned.Slots, schemas.Slot{Arm: arm, Task: fmt.Sprintf("t%d", task), Counted: true})
		}
	}
	f, err := a.forecast(context.Background(), planned)
	if err != nil {
		t.Fatal(err)
	}
	if f.Arms[0].Basis != schemas.BasisConfigHash || f.Arms[0].HistoryRuns != 4 || strings.Join(f.Arms[0].Experiments, ",") != "e_past" {
		t.Fatalf("arm A %+v", f.Arms[0])
	}
	if got := *f.Arms[0].CostUSD.Median; got < 0.3999 || got > 0.4001 {
		t.Fatalf("arm A median cost %v, want 4 x 0.10 (the scripted runs at 9 left out)", got)
	}
	if f.Arms[1].Basis != schemas.BasisBudgetBound || *f.Arms[1].CostUSD.High != 0.8 || f.Concurrency != 2 {
		t.Fatalf("arm B %+v", f.Arms[1])
	}
	if f.Fit != schemas.FitFits || !strings.Contains(strings.Join(f.Notes, ";"), "4 synthetic runs") || f.PriceRef == "" {
		t.Fatalf("fit %s (%s) notes %v", f.Fit, f.FitReason, f.Notes)
	}
	var out bytes.Buffer
	printForecast(&out, f)
	for _, want := range []string{"arm A", "basis config_hash", "at most $0.8000", "budget $2.00: fits", "note: left out 4 synthetic runs"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("forecast output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestMatrixFromStore(t *testing.T) {
	st := forecastStore(t)
	addSealedExperiment(t, st, "e_mx", map[string]float64{"A": 0.1, "B": 0.2, "C": 0.3}, `{"kind":"message","action":"pass","upstream":"real","status":200}`)
	a := &app{store: st, prices: testPrices(t), logger: schemas.NoopLogger{}}
	mx, err := serveForecast{a: a}.Matrix(context.Background(), "e_mx", leaderboardMatrixQuery())
	if err != nil {
		t.Fatal(err)
	}
	if len(mx.Rows) != 3 || len(mx.Pairs) != 3 || mx.Separable || mx.Synthetic || mx.Tasks != 4 {
		t.Fatalf("matrix rows %d pairs %d separable %v synthetic %v tasks %d", len(mx.Rows), len(mx.Pairs), mx.Separable, mx.Synthetic, mx.Tasks)
	}
	if mx.Rows[2].Arm != "A" || mx.Rows[0].Arm != "B" {
		t.Fatalf("rank order %s %s %s, want B (cheaper of the two perfect arms) first and A last", mx.Rows[0].Arm, mx.Rows[1].Arm, mx.Rows[2].Arm)
	}
	var out bytes.Buffer
	printMatrix(&out, mx)
	for _, want := range []string{"matrix e_mx", "Cannot separate any two arms", "B vs C", "pass^k"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("matrix output lacks %q:\n%s", want, out.String())
		}
	}
	scripted, err := a.matrix(context.Background(), "e_scripted", leaderboardMatrixQuery())
	if err != nil || !scripted.Synthetic {
		t.Fatalf("scripted-model experiment not marked synthetic: %v %+v", err, scripted)
	}
	if _, err := a.matrix(context.Background(), "e_none", leaderboardMatrixQuery()); err == nil {
		t.Fatal("unknown experiment answered")
	}
}

func TestAllowUnpricedModels(t *testing.T) {
	mx := &schemas.MatrixSpec{Models: []schemas.MatrixModel{{Model: "x"}}}
	e := &schemas.Experiment{Matrix: mx}
	allowUnpricedModels(e, true)
	if !e.Matrix.AllowUnpriced || mx.AllowUnpriced {
		t.Fatalf("allow not recorded on a copy: %+v %+v", e.Matrix, mx)
	}
	plain := &schemas.Experiment{}
	allowUnpricedModels(plain, true)
	if plain.Matrix != nil {
		t.Fatal("an experiment without a matrix got one")
	}
}

func TestMatrixExperimentDecodes(t *testing.T) {
	e, err := loadExperiment(filepath.Join("..", "..", "experiments", "matrix-models.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Matrix == nil || len(e.Matrix.Models) != 3 || e.Matrix.Models[1].Provider != "openai" || len(e.Arms) != 0 {
		t.Fatalf("matrix %+v arms %v", e.Matrix, e.Arms)
	}
	if _, err := loadExperiment(writeTemp(t, "name: x\nsuite: s@1\nbase: agents/codex.yaml\nprimary: {condition: clean, metric: verified_success}\nconditions: [{id: clean, tasks: all, trials: 1}]\n")); err == nil {
		t.Fatal("an experiment with neither arms nor matrix decoded")
	}
}

func TestRangeText(t *testing.T) {
	lo, mid, hi := 0.5, 1.25, 3.0
	tests := []struct {
		r    schemas.ForecastRange
		want string
	}{
		{schemas.ForecastRange{Kind: schemas.RangeHistory, Low: &lo, Median: &mid, High: &hi}, "$0.5000 to $3.00 (median $1.25)"},
		{schemas.ForecastRange{Kind: schemas.RangeUpperBound, High: &hi}, "at most $3.00"},
		{schemas.ForecastRange{Kind: schemas.RangeUnknown, Reason: "no history, cannot estimate"}, "unknown: no history, cannot estimate"},
	}
	for _, tt := range tests {
		if got := rangeText(tt.r, moneyText); got != tt.want {
			t.Errorf("rangeText = %q, want %q", got, tt.want)
		}
	}
	for s, want := range map[float64]string{45: "45s", 600: "10m", 3725: "1h2m", 3600: "1h", 370: "6m10s"} {
		if got := durationText(s); got != want {
			t.Errorf("durationText(%v) = %q, want %q", s, got, want)
		}
	}
}
