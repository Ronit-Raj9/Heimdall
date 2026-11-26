package traceinbox

import (
	"strings"
	"testing"
)

// F5.4: a malformed signature gets a stable id instead of a panic.
func TestItemIDShortSignature(t *testing.T) {
	for _, sig := range []string{"", "f_", "f_ab"} {
		got := ItemID(sig)
		if len(got) != 16 || !strings.HasPrefix(got, "inc-") {
			t.Fatalf("ItemID(%q) = %q, want inc- and 12 characters", sig, got)
		}
		if ItemID(sig) != got {
			t.Fatalf("ItemID(%q) is not stable", sig)
		}
	}
	if got := ItemID("f_0123456789abcdef"); got != "inc-0123456789ab" {
		t.Fatalf("ItemID of a real signature = %q", got)
	}
}
