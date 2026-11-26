package improve

import (
	"context"
	"testing"
)

// TestBusyWhileImprovementRuns: AnyRunning is true exactly while the runner
// drives a loop, so a hosted org runtime is not evicted under one.
func TestBusyWhileImprovementRuns(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	if r.AnyRunning() {
		t.Fatal("idle runner busy")
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.mu.Lock()
	r.running = map[string]context.CancelFunc{"imp_x": cancel}
	r.mu.Unlock()
	if !r.AnyRunning() {
		t.Fatal("running runner not busy")
	}
	var nilRunner *Runner
	if nilRunner.AnyRunning() {
		t.Fatal("nil runner busy")
	}
}
