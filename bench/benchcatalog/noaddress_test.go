package benchcatalog

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

// TestFetchRefusalHasNoAddress: the dataset fetcher's dial refusal reaches
// the member without the private address a host resolved to (fix wave 2,
// N104), and errors.Is still sees ErrFetchRefused.
func TestFetchRefusalHasNoAddress(t *testing.T) {
	_, err := NewFetcher().httpClient().Get("https://192.168.7.9:1/data.jsonl")
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a url.Error", err)
	}
	if !errors.Is(err, ErrFetchRefused) {
		t.Fatalf("err = %v, want ErrFetchRefused", err)
	}
	if msg := ue.Err.Error(); strings.Contains(msg, "192.168.7.9") || !strings.Contains(msg, "a private address") {
		t.Fatalf("refusal text = %q", msg)
	}
}
