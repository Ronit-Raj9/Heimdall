package handlers

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// researchIndex is a two-arm research smoke experiment (W4): A (oracle)
// verifies 2 of 2 with grounded citations, B (noop) 0 of 2 and cites nothing.
func researchIndex() *fakeIndex {
	m := &schemas.Manifest{ExpID: "e_rs", SuiteID: "acme-research", SuiteVersion: "1.0.0",
		Arms:       map[string]schemas.ArmManifest{"A": {Label: "oracle"}, "B": {Label: "noop"}},
		Experiment: schemas.Experiment{Smoke: true}}
	obs := func(v float64) string { return fmt.Sprintf(`{"state":"observed","v":%g,"evidence":"outcome"}`, v) }
	na := `{"state":"not_applicable"}`
	files := map[string]map[string][]byte{
		"r_a1": {FileMetrics: []byte(`{"verified_success":` + obs(1) + `,"searches":` + obs(2) + `,"sources_cited":` + obs(1) + `,"citations_grounded":` + obs(1) + `,"citation_precision":` + obs(1) + `,"external_unmatched":` + obs(0) + `}`)},
		"r_a2": {FileMetrics: []byte(`{"verified_success":` + obs(1) + `,"searches":` + obs(4) + `,"sources_cited":` + obs(3) + `,"citations_grounded":` + obs(1) + `,"citation_precision":` + obs(0.5) + `,"external_unmatched":` + obs(1) + `}`)},
		"r_b1": {FileMetrics: []byte(`{"verified_success":` + obs(0) + `,"searches":` + obs(0) + `,"sources_cited":` + obs(0) + `,"citations_grounded":` + na + `,"citation_precision":` + na + `,"external_unmatched":` + obs(0) + `}`)},
		"r_b2": {FileMetrics: []byte(`{"verified_success":` + obs(0) + `,"searches":` + obs(0) + `,"sources_cited":` + obs(0) + `,"citations_grounded":` + na + `,"citation_precision":` + na + `,"external_unmatched":` + obs(0) + `}`)},
	}
	run := func(id, arm string) RunView {
		return RunView{Slot: schemas.Slot{SlotID: "s_" + id, ExpID: "e_rs", Arm: arm, Condition: "clean", Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_rs", Attempt: 1}, Sealed: true}
	}
	return &fakeIndex{manifests: map[string]*schemas.Manifest{"e_rs": m}, files: files,
		runs: []RunView{run("r_a1", "A"), run("r_a2", "A"), run("r_b1", "B"), run("r_b2", "B")}}
}

func TestExperimentResearch(t *testing.T) {
	idx := researchIndex()
	out, err := ExperimentResearch(idx, idx.manifests["e_rs"], "clean")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Research || !out.Replay || !out.Synthetic || len(out.Arms) != 2 {
		t.Fatalf("summary %+v", out)
	}
	near := func(p *float64, want float64) bool { return p != nil && *p > want-1e-9 && *p < want+1e-9 }
	a, b := out.Arms[0], out.Arms[1]
	if a.Passed != 2 || a.Decided != 2 || !near(a.Verified, 1) || !near(a.Searches, 3) || !near(a.SourcesCited, 2) || !near(a.CitationsGrounded, 1) || !near(a.CitationPrecision, 0.75) || !near(a.Unmatched, 0.5) {
		t.Errorf("arm A %+v", a)
	}
	if !near(b.Verified, 0) || !near(b.Searches, 0) || b.CitationsGrounded != nil || b.CitationPrecision != nil || b.Label != "noop" {
		t.Errorf("arm B %+v", b)
	}
	live := *idx.manifests["e_rs"]
	live.Experiment.External = schemas.ExternalLive
	if out, _ := ExperimentResearch(idx, &live, "clean"); out.Replay {
		t.Error("a live experiment reported as replay")
	}
}

func TestResearchRoute(t *testing.T) {
	h := NewResearchHandler(testMode, researchIndex())
	var ctx fasthttp.RequestCtx
	ctx.SetUserValue("id", "e_rs")
	h.getResearch(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil || ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	checkKeys(t, "ResearchSummary", body, loadKeys(t, "ResearchSummary"), true)
	for i, obj := range objectsAt(t, body, "arms[]") {
		checkKeys(t, fmt.Sprintf("arms #%d", i), obj, loadKeys(t, "ResearchArm"), false)
	}
	var missing fasthttp.RequestCtx
	missing.SetUserValue("id", "e_missing")
	h.getResearch(&missing)
	if missing.Response.StatusCode() != 404 {
		t.Errorf("missing experiment status %d", missing.Response.StatusCode())
	}
}

func TestResearchNotApplicable(t *testing.T) {
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_c": {ExpID: "e_c", Arms: map[string]schemas.ArmManifest{"A": {}}}},
		files: map[string]map[string][]byte{"r_1": {FileMetrics: []byte(`{"verified_success":{"state":"observed","v":1},"searches":{"state":"not_applicable"}}`)}},
		runs:  []RunView{{Slot: schemas.Slot{SlotID: "s_1", ExpID: "e_c", Arm: "A", Condition: "clean", Counted: true}, Run: schemas.Run{RunID: "r_1", ExpID: "e_c", Attempt: 1}, Sealed: true}}}
	out, err := ExperimentResearch(idx, idx.manifests["e_c"], "clean")
	if err != nil || out.Research || len(out.Arms) != 0 {
		t.Fatalf("summary %+v, %v", out, err)
	}
}
