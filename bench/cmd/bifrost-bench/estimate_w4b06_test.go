package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestEstimateDeadlineIsNotRefusal checks an estimate that runs out of time
// while planning answers the deadline itself, not a plan refusal, so the
// handler can ask the caller to try again.
func TestEstimateDeadlineIsNotRefusal(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	if err := os.CopyFS(filepath.Join(dir, "benchmarks", "coding-core"), os.DirFS(filepath.Join("..", "..", "benchmarks", "coding-core"))); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(context.Background(), filepath.Join(dir, "deploy", "bench.yaml"), appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.logger = schemas.NoopLogger{}
	exp := &schemas.Experiment{
		Name: "deadline-proof", Suite: "coding-core@1.0.0", Base: "agents/toy.yaml",
		Arms:       map[string]schemas.ArmSpec{"A": {Label: "a"}, "B": {Label: "b", Set: map[string]any{"model": "other"}}},
		Primary:    schemas.Endpoint{Condition: "clean", Metric: "verified_success"},
		Conditions: []schemas.ConditionSpec{{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1}},
		Seed:       1,
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err = serveForecast{a: a}.Estimate(ctx, exp)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want the deadline, got %v", err)
	}
	if errors.Is(err, handlers.ErrPlanRefused) {
		t.Fatalf("a deadline was answered as a refusal: %v", err)
	}
}
