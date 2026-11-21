package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

type fakePairedSource struct {
	m    map[string]*schemas.Manifest
	runs map[string][]schemas.SealedRun
}

func (f fakePairedSource) LoadManifest(_ context.Context, id string) (*schemas.Manifest, error) {
	if m, ok := f.m[id]; ok {
		return m, nil
	}
	return nil, errors.New("no such experiment")
}

func (f fakePairedSource) SealedRuns(_ context.Context, id, _ string) ([]schemas.SealedRun, error) {
	return f.runs[id], nil
}

func pairedTestRun(task string, v float64) schemas.SealedRun {
	return schemas.SealedRun{Slot: schemas.Slot{Task: task, Arm: "A", Condition: "clean", Counted: true},
		Run: schemas.Run{Outcome: schemas.OutcomePass}, Metrics: map[string]schemas.Value{"verified_success": schemas.Observed(v, "")}}
}

func TestPairedRun(t *testing.T) {
	man := func(realism, hash string) *schemas.Manifest {
		return &schemas.Manifest{SuiteID: "s", SuiteVersion: "1", SuiteLockHash: "l", Experiment: schemas.Experiment{Realism: realism},
			Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: hash}}}
	}
	src := fakePairedSource{
		m: map[string]*schemas.Manifest{"e_std": man("", "h"), "e_neu": man(schemas.RealismNeutral, "h"), "e_other": man("", "x")},
		runs: map[string][]schemas.SealedRun{
			"e_std": {pairedTestRun("t1", 1), pairedTestRun("t2", 1)},
			"e_neu": {pairedTestRun("t1", 0), pairedTestRun("t2", 1)},
		},
	}
	var out bytes.Buffer
	if code := pairedRun(context.Background(), &out, src, "e_std", "e_neu", "A", "clean", "verified_success", "original", false); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if s := out.String(); !strings.Contains(s, "paired tasks 2") || !strings.Contains(s, "-0.5000") || !strings.Contains(s, "realism neutral") {
		t.Fatalf("output %q", s)
	}
	if code := pairedRun(context.Background(), &out, src, "e_std", "e_other", "A", "clean", "verified_success", "original", false); code == exitOK {
		t.Fatal("different configs paired")
	}
}
