package onboard

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// A link whose host does not exist gives a plain reason, not the Go error chain.
func TestPlainFetchErrorDNS(t *testing.T) {
	env := testEnv(t)
	env.Fetch = func(ctx context.Context, raw string) (string, []byte, error) {
		return "", nil, errors.Join(errors.New("fetch https://nowhere.example/x.csv: Get \"https://nowhere.example/x.csv\""),
			&net.DNSError{Err: "no such host", Name: "nowhere.example", IsNotFound: true})
	}
	p, err := env.Peek(context.Background(), Input{URL: "https://nowhere.example/x.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if p.FetchErr != "the address does not exist; check the link" {
		t.Fatalf("FetchErr %q", p.FetchErr)
	}
	for _, c := range []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, "the site did not answer in time"},
		{errors.New("fetch https://a.example/x: HTTP 404 (is the data public? gated datasets must be downloaded and uploaded)"), "the site answered 404"},
		{errors.New("fetch https://a.example/x: connection refused"), "the site could not be reached"},
	} {
		if got := plainFetchError(c.err); got != c.want {
			t.Errorf("plainFetchError(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// An unpacked zip is named after the uploaded file, not the timestamp folder.
func TestPeekZipUsesUploadName(t *testing.T) {
	env := testEnv(t)
	dst := filepath.Join(env.Root, "uploads", "onboard", "20261126-164742")
	if err := copyTree(filepath.Join("..", "importers", "harbor", "testdata", "tb"), dst); err != nil {
		t.Fatal(err)
	}
	in := Input{Upload: "onboard/20261126-164742", Name: "My Bench.zip"}
	inv, err := Inspect(context.Background(), env, in, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv.Suggest.Suite != "my-bench" || strings.Contains(inv.Title, "20261126") {
		t.Fatalf("suggested id %q title %q", inv.Suggest.Suite, inv.Title)
	}
}
