package livescore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// failSave is a FileStore whose session writes fail while fail is set.
type failSave struct {
	*traceinbox.FileStore
	fail bool
}

func (f *failSave) SaveSession(id string, s *traceimport.Session, preview []byte) error {
	if f.fail {
		return errors.New("disk full")
	}
	return f.FileStore.SaveSession(id, s, preview)
}

func testCandidate() *candidate {
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return &candidate{
		rec:     &SessionRecord{ID: "s1", Version: "v2", Signature: "f_0123456789abcdef", Severity: 2, Ended: at, Flags: []string{"abandoned"}},
		session: &traceimport.Session{ID: "s1", Format: "bifrost"},
		reasons: []string{"abandoned"},
	}
}

// A failed payload write publishes no item, and an item published without
// a payload (by an older producer) is repaired on its next occurrence.
func TestPushInboxSavesPayloadBeforeItem(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	store := &failSave{FileStore: traceinbox.NewFileStore(filepath.Join(t.TempDir(), "inbox")), fail: true}
	if _, err := pushInbox(store, []*candidate{testCandidate()}, 3, "", "", now); err == nil {
		t.Fatal("push with a failing session write reported success")
	}
	if items, err := store.Items(); err != nil || len(items) != 0 {
		t.Fatalf("items %v (err %v), want none published without a payload", items, err)
	}
	// Simulate an item an older producer published without its payload.
	id := traceinbox.ItemID("f_0123456789abcdef")
	if err := store.Update(func(items []traceinbox.Item) ([]traceinbox.Item, error) {
		return append(items, traceinbox.Item{ID: id, State: traceinbox.StateNew, Signature: "f_0123456789abcdef", Traces: []traceinbox.TraceRef{}}), nil
	}); err != nil {
		t.Fatal(err)
	}
	store.fail = false
	if _, err := pushInbox(store, []*candidate{testCandidate()}, 3, "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(id); err != nil {
		t.Fatalf("payload not repaired: %v", err)
	}
}
