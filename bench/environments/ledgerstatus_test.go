package environments

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A status that could not be copied leaves a status marking the ledger
// incomplete, never no file (which collection reads as a legacy proxy).
func TestLedgerStatusCopyFailureFailsClosed(t *testing.T) {
	out := filepath.Join(t.TempDir(), schemas.ToolLedgerStatusFile)
	if err := writeLedgerStatusFailure(out, "copy failed: no such container", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var st schemas.ToolLedgerStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if st.Incomplete() == "" {
		t.Fatalf("status %+v does not mark the ledger incomplete", st)
	}
}
