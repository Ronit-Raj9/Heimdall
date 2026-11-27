package onboard

import (
	"strings"
	"testing"
)

// The next-step texts must point to a page that exists, never end in a dead
// end.
func TestNextTextsPointToABuilder(t *testing.T) {
	if strings.Contains(openAPINext, "not built yet") || !strings.Contains(openAPINext, "Tasks from your API") {
		t.Fatalf("openAPINext: %q", openAPINext)
	}
	if !strings.Contains(githubNext, "Commits in your repository") {
		t.Fatalf("githubNext: %q", githubNext)
	}
	for _, s := range []string{openAPINext, githubNext} {
		if strings.ContainsAny(s, "—–") {
			t.Fatalf("long dash in %q", s)
		}
	}
}
