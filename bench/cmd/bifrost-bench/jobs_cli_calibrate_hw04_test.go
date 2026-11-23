package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Fix wave 3, N286: "Run the check" on a benchmark with no labels and
// without the benchmark's own answers is refused when the job is created,
// in plain words (400), not after the job starts with the CLI's wording.
func TestCalibrateRunRefusesWithoutLabels(t *testing.T) {
	a, _, s := judgedFixture(t)
	old := calibrateLoadSuite
	calibrateLoadSuite = func(context.Context, *app, string) (*schemas.Suite, error) { return s, nil }
	t.Cleanup(func() { calibrateLoadSuite = old })
	env := jobEnv{App: a, OrgID: "org1", Hosted: true}
	const want = "This benchmark has no labelled answers yet. Add labels on the Grader accuracy tab first."
	for _, params := range []string{`{"suite":"qa","yes":true}`, `{"suite":"qa","estimate":true}`} {
		_, _, err := validateCalibrateRun(context.Background(), env, json.RawMessage(params))
		if err == nil || !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "judge label") {
			t.Fatalf("%s: err = %v, want the plain refusal", params, err)
		}
	}
	// The benchmark's own right and wrong answers are enough.
	if _, _, err := validateCalibrateRun(context.Background(), env, json.RawMessage(`{"suite":"qa","yes":true,"seed_controls":true}`)); err != nil {
		t.Fatalf("seed controls: %v", err)
	}
	// One label is enough.
	it, err := calibration.NewItem(schemas.CalibrationLabelRequest{Task: "q1", Answer: "Max", Label: "correct", Labeller: "ana"}, schemas.CalibrationSourceLabel, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := calibration.Append(s.Root, []schemas.CalibrationItem{it}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateCalibrateRun(context.Background(), env, json.RawMessage(`{"suite":"qa","yes":true}`)); err != nil {
		t.Fatalf("with a label: %v", err)
	}
}
