//go:build unix

package store

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Audit 1-A-06: the pre-seal secret scan must classify an entry before it
// opens it. A named pipe in the run folder used to block Seal in os.ReadFile
// while the experiment's chain lock was held.
func TestSealRefusesFIFOWithoutOpening(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r := mustNewRun(t, s, m, "s_a1", "r_1", 1, schemas.OutcomePass)
	if err := syscall.Mkfifo(filepath.Join(s.runDir(r), "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Seal(context.Background(), r)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "pipe is not a regular file") {
			t.Fatalf("err = %v, want non-regular refusal naming pipe", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Seal blocked opening a named pipe")
	}
	if sealed, err := isSealed(s.runDir(r)); err != nil || sealed {
		t.Fatalf("sealed = %v, %v after refusal", sealed, err)
	}
}
