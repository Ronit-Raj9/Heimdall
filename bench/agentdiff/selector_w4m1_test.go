package agentdiff

import (
	"errors"
	"strings"
	"testing"
)

// TestAgentDiffTooFewVersionsPlain checks that an agent with fewer than two
// versions gets a plain sentence without internal words (wave 4, p02-6).
func TestAgentDiffTooFewVersionsPlain(t *testing.T) {
	_, _, err := latestTwo(collectArms(flipFixture()), "nobody", "")
	if err == nil || !errors.Is(err, ErrBadQuery) {
		t.Fatalf("err %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "config hash") {
		t.Fatalf("internal wording in %q", msg)
	}
	if !strings.Contains(msg, "nobody has 0 versions with saved runs; two are needed.") {
		t.Fatalf("sentence %q", msg)
	}
}
