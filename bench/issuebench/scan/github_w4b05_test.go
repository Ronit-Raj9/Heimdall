package scan

import (
	"net/http"
	"testing"
)

// p11-2: the 401 text says the issue scan needs a working connection, even
// for public repositories.
func TestScan401Text(t *testing.T) {
	got := (&APIError{Status: http.StatusUnauthorized}).Error()
	want := "GitHub refused the connected account. Finding issues needs a working GitHub connection, even for public repositories: reconnect GitHub in Settings > GitHub."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
