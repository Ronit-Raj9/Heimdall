package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

// recordLog keeps every job log line so a test can read what the run said.
type recordLog struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordLog) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}
func (r *recordLog) Printf(f string, a ...any)   { r.add(f, a...) }
func (r *recordLog) Warnf(f string, a ...any)    { r.add(f, a...) }
func (r *recordLog) Errorf(f string, a ...any)   { r.add(f, a...) }
func (r *recordLog) Write(p []byte) (int, error) { r.add("%s", p); return len(p), nil }
func (r *recordLog) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// A debug experiment (replay, fork, step run) is never analysed at its end:
// its run is never counted, so an analysis always decided "invalid" and the
// page said "Test was broken" (fix wave 3, N247).
func TestDebugExperimentNotAnalysed(t *testing.T) {
	a, _ := jobsProject(t)
	ctx := context.Background()
	m := &schemas.Manifest{ExpID: "e_fork_dbg", Experiment: schemas.Experiment{
		Debug: &schemas.DebugSpec{Mode: "fork", FromRun: "r_src", Call: 1, Edit: &schemas.DebugEdit{Message: "hi"}},
	}}
	h, err := engine.ManifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Hash = h
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	l := newLauncher(a, nil)
	log := &recordLog{}
	// The test app has no suite loader, so the scheduler itself errors; the
	// point is what happens after it: no analysis is tried.
	out, _ := l.execute(ctx, m, l.liveRunFor(m.ExpID, ""), log, nil)
	if res := out.(expRunResult); res.Decision != "" || res.AnalysisID != "" {
		t.Fatalf("debug experiment analysed: %+v", res)
	}
	if strings.Contains(log.text(), "analyze:") || strings.Contains(log.text(), "analysis ") {
		t.Fatalf("debug experiment analysed:\n%s", log.text())
	}
	if an, _ := a.store.LatestAnalysis(ctx, m.ExpID); an != nil {
		t.Fatalf("analysis written for a debug experiment: %s %s", an.AnalysisID, an.Verdict.Decision)
	}
	if !strings.Contains(log.text(), "debug run: not scored") {
		t.Fatalf("log does not say the debug run is not scored:\n%s", log.text())
	}
}
