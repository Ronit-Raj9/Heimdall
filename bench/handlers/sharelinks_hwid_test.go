package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/sharelinks"
)

// failingShareIndex refuses every write, as a database that is down does.
type failingShareIndex struct{}

func (failingShareIndex) PutShareToken(context.Context, string, string, string) error {
	return errors.New("db: connection refused")
}

// TestShareCreateFailsClosedWithoutIndex (fix wave 3, N293 review): when
// the share index cannot record a new link, create answers an error, hands
// out no token, and the stored link is revoked, so no link that nobody can
// open (or that opens later through a backfill) is left behind.
func TestShareCreateFailsClosedWithoutIndex(t *testing.T) {
	r, root := shareRouter(t)
	RegisterShareIndex(filepath.Dir(root), failingShareIndex{})
	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() < 500 {
		t.Fatalf("create with a failing index: %d %s", res.StatusCode(), res.Body())
	}
	shares, err := sharelinks.Open(root).List("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) == 0 {
		t.Fatal("no link stored: the test does not reach the index write")
	}
	for _, sh := range shares {
		if sh.RevokedAt == nil {
			t.Fatalf("link %s left active after the index write failed", sh.ID)
		}
	}
}
