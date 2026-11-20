package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// resultsFixture is a two-arm experiment on 4 tasks, 2 trials, clean and one
// fault condition, with sealed runs. Arm A passes tasks 1-3, arm B task 1.
func resultsFixture() EEEInput {
	m := &schemas.Manifest{ExpID: "e_fx", SuiteID: "coding-core", SuiteVersion: "1.0.0", SuiteLockHash: "lockhash",
		Hash: "manifesthash", BenchVersion: "test", CreatedAt: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
		Experiment: schemas.Experiment{Primary: schemas.Endpoint{Condition: "clean", Metric: "verified_success"}},
		Arms:       map[string]schemas.ArmManifest{}}
	for _, a := range []string{"A", "B"} {
		m.Arms[a] = schemas.ArmManifest{Label: "label " + a, ConfigHash: "cfg" + a,
			Config: schemas.AgentConfig{ID: "codex", Model: "gpt-x-" + strings.ToLower(a), Adapter: "command", Image: "bench/codex:1",
				Budgets: schemas.Budgets{WallS: 900, Turns: 40, USD: 2}, Gateway: schemas.GatewayPolicy{Provider: "openai"}}}
	}
	var runs []schemas.SealedRun
	for _, cond := range []string{"clean", "model-outage"} {
		for task := 1; task <= 4; task++ {
			for trial := 1; trial <= 2; trial++ {
				for _, a := range []string{"A", "B"} {
					slot := schemas.Slot{SlotID: fmt.Sprintf("s_%s_%s_%d_%d", cond, a, task, trial), PairID: fmt.Sprintf("p_%s_%d_%d", cond, task, trial),
						ExpID: m.ExpID, Suite: "coding-core", Task: fmt.Sprintf("t%02d", task), TaskVersion: "1.0.0", Arm: a, Condition: cond, Trial: trial, Counted: true}
					m.Slots = append(m.Slots, slot)
					pass := (a == "A" && task <= 3) || (a == "B" && task == 1)
					if cond != "clean" && task == 3 {
						pass = false
					}
					outcome, y := schemas.OutcomeFail, 0.0
					if pass {
						outcome, y = schemas.OutcomePass, 1
					}
					runs = append(runs, schemas.SealedRun{Slot: slot, SealHash: "seal-" + slot.SlotID, GradeSource: "original",
						Run: schemas.Run{RunID: "r_" + slot.SlotID, Outcome: outcome, Attempt: 1, ManifestHash: m.Hash,
							Timing: schemas.RunTiming{Started: time.Date(2026, 11, 17, 10, task, trial, 0, time.UTC)}},
						Grade: &schemas.Grade{Passed: pass},
						Metrics: map[string]schemas.Value{
							"verified_success":         schemas.Observed(y, schemas.EvidenceOutcome),
							leaderboard.MetricCostUSD:  schemas.Observed(0.02*float64(task), schemas.EvidenceGateway),
							leaderboard.MetricTokens:   schemas.Observed(1000, schemas.EvidenceGateway),
							leaderboard.MetricWallTime: schemas.Observed(30, schemas.EvidenceOutcome),
							leaderboard.MetricSteps:    schemas.Unknown("no trace", schemas.EvidenceOutcome),
						}})
				}
			}
		}
	}
	return EEEInput{Manifest: m, Runs: runs, Synthetic: map[string]bool{"A": false, "B": false}}
}

func TestEEERecordsValidate(t *testing.T) {
	in := resultsFixture()
	recs, err := EEE(context.Background(), in, EEEOptions{Organization: "Acme", Now: time.Unix(1763460000, 0), BenchVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("%d records, want one per arm", len(recs))
	}
	for _, r := range recs {
		if err := ValidateEEE(r); err != nil {
			t.Fatalf("record %v does not validate: %v", r["evaluation_id"], err)
		}
	}
	b, err := json.MarshalIndent(recs[0], "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "eee/arm-A.json", append(b, '\n'))
	results := recs[0]["evaluation_results"].([]any)
	names := map[string]bool{}
	for _, r := range results {
		names[r.(map[string]any)["evaluation_result_id"].(string)] = true
	}
	for _, want := range []string{"heimdall.coding-core.clean.heimdall.verified_success", "heimdall.coding-core.model-outage.heimdall.verified_success",
		"heimdall.coding-core.clean.heimdall.pass_hat_k", "heimdall.coding-core.clean.heimdall.cost_per_verified_success_usd"} {
		if !names[want] {
			t.Errorf("no result %s in %v", want, names)
		}
	}
	if names["heimdall.coding-core.clean.heimdall.steps_median"] {
		t.Error("a metric no run observed must be left out, not written as zero")
	}
}

func TestEEERefusesSynthetic(t *testing.T) {
	in := resultsFixture()
	in.Synthetic["B"] = true
	if _, err := EEE(context.Background(), in, EEEOptions{}); !errors.Is(err, ErrSynthetic) {
		t.Fatalf("err = %v, want ErrSynthetic", err)
	}
	recs, err := EEE(context.Background(), in, EEEOptions{AllowSynthetic: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		d := r["source_metadata"].(map[string]any)["additional_details"].(map[string]string)
		if r["evaluation_id"].(string) == "heimdall.coding-core/e_fx/B" && (d["heimdall_synthetic"] != "true" || d["heimdall_note"] == "") {
			t.Errorf("synthetic arm not labelled: %v", d)
		}
	}
}

func TestValidateEEERejectsBadRecords(t *testing.T) {
	recs, err := EEE(context.Background(), resultsFixture(), EEEOptions{})
	if err != nil {
		t.Fatal(err)
	}
	clone := func() map[string]any {
		b, _ := json.Marshal(recs[0])
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return m
	}
	missing := clone()
	delete(missing, "model_info")
	extra := clone()
	extra["heimdall"] = "not allowed at the top level"
	badEnum := clone()
	badEnum["source_metadata"].(map[string]any)["evaluator_relationship"] = "self"
	outside := clone()
	outside["evaluation_results"].([]any)[0].(map[string]any)["score_details"].(map[string]any)["score"] = 7.0
	for name, rec := range map[string]map[string]any{"missing model_info": missing, "extra top-level field": extra, "bad enum": badEnum, "score outside bounds": outside} {
		if err := ValidateEEE(rec); err == nil {
			t.Errorf("%s: validated", name)
		}
	}
	if _, err := EEE(context.Background(), resultsFixture(), EEEOptions{Relationship: "self"}); err == nil {
		t.Error("relationship self accepted")
	}
}

func TestRunTableJSONLAndCSV(t *testing.T) {
	in := resultsFixture()
	rows := RunTable(in)
	if len(rows) != len(in.Runs) {
		t.Fatalf("%d rows for %d runs", len(rows), len(in.Runs))
	}
	var j, c bytes.Buffer
	if err := WriteJSONL(&j, rows[:2]); err != nil {
		t.Fatal(err)
	}
	if err := WriteCSV(&c, rows[:2]); err != nil {
		t.Fatal(err)
	}
	golden(t, "runtable/rows.jsonl", j.Bytes())
	golden(t, "runtable/rows.csv", c.Bytes())
	if !strings.Contains(j.String(), `"steps":null`) {
		t.Error("an unobserved metric must be null in JSONL")
	}
}

func TestCSVSafe(t *testing.T) {
	if csvSafe("=HYPERLINK(1)") != "'=HYPERLINK(1)" || csvSafe("ok") != "ok" {
		t.Fatal("formula cell not defused")
	}
}

// TestRunRowMatchesContract holds export.RunRow's JSON keys to the key list
// the UI's ExportRunRow type is checked against
// (handlers/testdata/contract/ExportRunRow.keys; "?" marks omitempty).
func TestRunRowMatchesContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "handlers", "testdata", "contract", "ExportRunRow.keys"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{} // key -> optional
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		want[strings.TrimSuffix(l, "?")] = strings.HasSuffix(l, "?")
	}
	typ := reflect.TypeOf(RunRow{})
	got := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		got[name] = strings.Contains(opts, "omitempty")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RunRow keys %v, contract %v", got, want)
	}
}

// An experiment whose newest verdict is INVALID is never published as EEE
// (finding 1-D-05).
func TestEEERefusesInvalid(t *testing.T) {
	in := resultsFixture()
	in.Invalid = true
	if _, err := EEE(context.Background(), in, EEEOptions{Organization: "Acme"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err %v, want ErrInvalid", err)
	}
}

// A run whose seal failed verification is excluded from the score and never
// listed among the result's provenance seals (finding 1-D-11).
func TestEEEProvenanceLeavesOutFailedSeal(t *testing.T) {
	in := resultsFixture()
	bad := in.Runs[0]
	in.Runs[0].Problems = []string{"seal does not verify: file metrics.json changed"}
	recs, err := EEE(context.Background(), in, EEEOptions{Organization: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range recs {
		b, _ := json.Marshal(rec)
		var r struct {
			Source struct {
				Details map[string]string `json:"additional_details"`
			} `json:"source_metadata"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		d := r.Source.Details
		if d["heimdall_arm"] != bad.Slot.Arm {
			continue
		}
		if strings.Contains(d["heimdall_seal_hashes"], bad.SealHash+"\"") || d["heimdall_runs_sealed"] != "15" ||
			d["heimdall_runs_excluded"] != "1" || !strings.Contains(d["heimdall_excluded_seal_hashes"], bad.SealHash) {
			t.Fatalf("provenance %v: want %s only among excluded seals", d, bad.SealHash)
		}
		return
	}
	t.Fatal("no record for the arm")
}

// Held-out task runs stay out of the default run table and every row names
// its split (finding 1-D-02); split all is the explicit private export.
func TestRunTableHeldOutSplit(t *testing.T) {
	in := resultsFixture()
	in.Manifest.HeldOutTasks = []string{"t04"}
	for _, r := range RunTable(in) {
		if r.Task == "t04" || r.Split != schemas.SplitPublic {
			t.Fatalf("public table has row %s task %s split %s", r.RunID, r.Task, r.Split)
		}
	}
	in.Split = SplitAll
	held := 0
	for _, r := range RunTable(in) {
		if r.Task == "t04" {
			held++
			if r.Split != schemas.SplitHeldOut {
				t.Fatalf("row %s split %q", r.RunID, r.Split)
			}
		}
	}
	if held != 8 {
		t.Fatalf("held-out rows %d, want 8 with split all", held)
	}
}

// The flat table's verified_success is the board's (the observed metric),
// and the outcome's own score has its own name (finding 1-D-08).
func TestRunTableVerifiedSuccessMatchesBoard(t *testing.T) {
	in := resultsFixture()
	in.Runs[0].Run.Outcome = schemas.OutcomeTimeout
	in.Runs[0].Metrics["verified_success"] = schemas.Observed(1, schemas.EvidenceOutcome)
	for _, r := range RunTable(in) {
		if r.RunID == in.Runs[0].Run.RunID {
			if r.Success == nil || *r.Success != 1 || r.OutcomeScore == nil || *r.OutcomeScore != 0 {
				t.Fatalf("verified_success %v outcome_score %v, want 1 and 0", r.Success, r.OutcomeScore)
			}
			return
		}
	}
	t.Fatal("row missing")
}
