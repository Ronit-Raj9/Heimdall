package traceimport

import (
	"strings"
	"testing"
)

// TestDraftUnknownKindPlain checks that an unknown task type is refused in
// plain words with no command line flag (wave 4, N78).
func TestDraftUnknownKindPlain(t *testing.T) {
	_, err := WriteDraft(DraftOptions{Session: &Session{}, Kind: "nope"})
	if err == nil {
		t.Fatal("unknown kind accepted")
	}
	want := "Unknown task type nope. Choose one of " + strings.Join(Kinds(), ", ") + "."
	if err.Error() != want {
		t.Fatalf("err = %q, want %q", err.Error(), want)
	}
}
