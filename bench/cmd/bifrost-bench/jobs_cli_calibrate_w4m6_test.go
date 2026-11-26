package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// localCalibrateNoLabels is a judged suite with no stored labels, run as a
// local web job.
func localCalibrateNoLabels(t *testing.T) (jobEnv, *schemas.Suite) {
	t.Helper()
	a, _, s := judgedFixture(t)
	pp := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(pp, []byte(`{"id":"t","source":"test","captured_at":"2026-11-14T00:00:00Z","models":{"judge-model":{"input_per_mtok":3,"output_per_mtok":15,"cache_read_per_mtok":0.3,"cache_write_per_mtok":3.75}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pt, err := evidence.LoadPrices(pp)
	if err != nil {
		t.Fatal(err)
	}
	a.prices = pt
	oldLoad := calibrateLoadSuite
	calibrateLoadSuite = func(context.Context, *app, string) (*schemas.Suite, error) { return s, nil }
	t.Cleanup(func() { calibrateLoadSuite = oldLoad })
	return jobEnv{App: a, ProjectRoot: a.cfg.Root}, s
}

func TestCalibrateEstimateWithSeedControlsNoLabels(t *testing.T) {
	env, s := localCalibrateNoLabels(t)
	res, log, err := runCal(env, `{"suite":"qa","estimate":true,"seed_controls":true}`)
	if err != nil {
		t.Fatalf("estimate with the box ticked: %v\n%s", err, log)
	}
	if r := res.(*calibrateResult); r.Calls <= 0 {
		t.Fatalf("estimate counted no calls: %+v", r)
	}
	if _, err := os.Stat(calibration.LabelsPath(s.Root)); err == nil {
		t.Fatal("the estimate saved the seed labels")
	}
}

func TestCalibrateNoLabelsWebSentence(t *testing.T) {
	env, _ := localCalibrateNoLabels(t)
	_, _, err := runCal(env, `{"suite":"qa","estimate":true}`)
	if err == nil || !strings.Contains(err.Error(), "This benchmark has no labels yet.") || strings.Contains(err.Error(), "judge label") {
		t.Fatalf("no labels: %v", err)
	}
}
