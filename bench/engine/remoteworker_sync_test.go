package engine

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A benchmark emitted after the shared worker's last project download was
// refused with "load suite: ... not found" (first real hosted From issues
// admit): check must sync the project first.
func TestWorkerCheckSyncsProjectFirst(t *testing.T) {
	synced := 0
	w := &Worker{opts: WorkerOptions{Logger: schemas.NoopLogger{}, Sync: func(context.Context) error { synced++; return nil }}}
	_, reason := w.check(context.Background(), &schemas.WorkerLease{ManifestHash: "x"}, &schemas.Manifest{Hash: "y"})
	if synced != 1 || reason == "" {
		t.Fatalf("synced %d times, reason %q", synced, reason)
	}
}
