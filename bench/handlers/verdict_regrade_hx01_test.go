package handlers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// hx01RegradeIndex is testIndex with committed regrades: every run's sealed
// gates.json says model_fidelity 1.0.0 fired on setup B, and each regrade
// recomputed it at 1.1.0 without firing.
type hx01RegradeIndex struct {
	*fakeIndex
	regrades []leaderboard.Regrade
	asked    []string // regrade ids RegradedRuns read
}

func (x *hx01RegradeIndex) Regrades(context.Context, string) ([]leaderboard.Regrade, error) {
	return x.regrades, nil
}

func (x *hx01RegradeIndex) RegradedRuns(_ context.Context, expID, regradeID string) ([]schemas.SealedRun, error) {
	x.asked = append(x.asked, regradeID)
	known := false
	for _, r := range x.regrades {
		known = known || r.ID == regradeID
	}
	if !known {
		return nil, fmt.Errorf("regrade %s: %w", regradeID, schemas.ErrNotFound)
	}
	var out []schemas.SealedRun
	for _, rv := range x.Runs(expID) {
		out = append(out, schemas.SealedRun{Slot: rv.Slot, Run: rv.Run, GradeSource: regradeID, Gates: []schemas.GateResult{
			{Gate: "model_fidelity", Version: "1.1.0", Effect: schemas.GateInvalidate, Fired: false},
			{Gate: "protected_files", Version: "1", Effect: schemas.GateVeto, Fired: false},
		}})
	}
	return out, nil
}

func hx01Index() *hx01RegradeIndex {
	idx := testIndex()
	for _, rv := range idx.runs {
		fired := rv.Slot.Arm == "B"
		idx.files[rv.Run.RunID][FileGates] = fmt.Appendf(nil,
			`[{"gate":"model_fidelity","version":"1.0.0","effect":"invalidate","fired":%t},{"gate":"protected_files","version":"1","effect":"veto","fired":false}]`, fired)
	}
	return &hx01RegradeIndex{fakeIndex: idx, regrades: []leaderboard.Regrade{
		{ID: "rg_old", CreatedAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC), Note: "first try"},
		{ID: "rg_new", CreatedAt: time.Date(2026, 11, 23, 9, 30, 0, 0, time.UTC), Note: "model check 1.1.0"},
	}}
}

// hx01Gate returns the verdict answer's summary of one gate.
func hx01Gate(t *testing.T, body map[string]any, name string) map[string]any {
	t.Helper()
	gates, _ := body["gates"].([]any)
	for _, g := range gates {
		if m, ok := g.(map[string]any); ok && m["gate"] == name {
			return m
		}
	}
	t.Fatalf("no gate %s in %v", name, body["gates"])
	return nil
}

// With a re-score selected, the verdict's checks list comes from that
// re-score's recomputed gates (version and fired counts), so it never
// contradicts the result it sits under. The original stays as sealed.
func TestVerdictGatesFollowRegradeSource(t *testing.T) {
	idx := hx01Index()
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)

	orig := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	g := hx01Gate(t, orig, "model_fidelity")
	fired, _ := g["fired"].(map[string]any)
	if g["version"] != "1.0.0" || fired["num"] != 2.0 || fired["den"] != 4.0 {
		t.Fatalf("original model_fidelity = %v, want 1.0.0 fired 2 of 4", g)
	}

	re := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict?grade_source=rg_new", nil, nil))
	if re["grade_source"] != "rg_new" {
		t.Fatalf("grade_source = %v", re["grade_source"])
	}
	g = hx01Gate(t, re, "model_fidelity")
	fired, _ = g["fired"].(map[string]any)
	if g["version"] != "1.1.0" || fired["num"] != 0.0 || fired["den"] != 4.0 {
		t.Fatalf("re-scored model_fidelity = %v, want 1.1.0 fired 0 of 4", g)
	}
	if _, ok := g["detail"]; ok {
		t.Fatalf("re-scored gate keeps the original's fired detail: %v", g["detail"])
	}
	if len(idx.asked) != 1 || idx.asked[0] != "rg_new" {
		t.Fatalf("regrades read = %v, want [rg_new]", idx.asked)
	}

	// Human reviews do not recompute gates: the sealed gates are read.
	rv := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict?grade_source=reviewed", nil, nil))
	if g := hx01Gate(t, rv, "model_fidelity"); g["version"] != "1.0.0" {
		t.Fatalf("reviewed model_fidelity = %v, want sealed 1.0.0", g)
	}
}

// The experiment's status names its newest committed re-score, so the page
// can show it by default; the plain verdict route keeps answering the
// original. An experiment without re-scores carries no field.
func TestStatusCarriesLatestRegrade(t *testing.T) {
	idx := hx01Index()
	d := fullDeps()
	d.index = idx
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	checkKeys(t, "ExperimentStatus", body, loadKeys(t, "ExperimentStatus"), true)
	lr, _ := body["latest_regrade"].(map[string]any)
	if lr == nil || lr["id"] != "rg_new" || lr["note"] != "model check 1.1.0" || lr["created_at"] != "2026-11-23T09:30:00Z" {
		t.Fatalf("latest_regrade = %v, want rg_new with its note and time", body["latest_regrade"])
	}
	checkKeys(t, "RegradeRef", lr, loadKeys(t, "RegradeRef"), false)
	if v := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil)); v["grade_source"] != "original" {
		t.Fatalf("plain verdict grade_source = %v, want original", v["grade_source"])
	}

	plain := decodeBody(t, newTestClient(t, fullDeps()).do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	if _, ok := plain["latest_regrade"]; ok {
		t.Fatalf("latest_regrade without regrades: %v", plain["latest_regrade"])
	}
	idx.regrades = nil
	none := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	if _, ok := none["latest_regrade"]; ok {
		t.Fatalf("latest_regrade with an empty list: %v", none["latest_regrade"])
	}
}
