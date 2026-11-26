package store

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// F5.5: only the run's own top-level seal.json skips the secret scan; a
// nested file of that name is scanned like any other.
func TestSealScansNestedSealJSON(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_nested")
	r := mustNewRun(t, s, m, "s_a1", "r_nested", 1, schemas.OutcomePass)
	mustWrite(t, s, r, "events.jsonl", `{"seq":1}`+"\n")
	mustWrite(t, s, r, "logs/seal.json", `{"authorization":"Bearer live-secret"}`+"\n")
	if _, err := s.Seal(context.Background(), r); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("seal err = %v, want ErrSealMismatch", err)
	}
}
