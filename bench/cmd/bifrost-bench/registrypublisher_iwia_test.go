package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegistryPublisherNeedsTokenFile: the publisher reads its bearer token
// from a file and refuses to start without one or with one shorter than 32
// characters; there is no flag that takes the token itself.
func TestRegistryPublisherNeedsTokenFile(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.token")
	if err := os.WriteFile(short, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := []string{"-auth-file", filepath.Join(dir, "push.auth"), "-manifest", filepath.Join(dir, "manifest"), "-temp", filepath.Join(dir, "tmp"), "-listen", "127.0.0.1:0"}
	for name, extra := range map[string][]string{
		"no token file":      nil,
		"missing token file": {"-token-file", filepath.Join(dir, "none")},
		"short token":        {"-token-file", short},
	} {
		if code := runRegistryPublisher(append(append([]string(nil), base...), extra...)); code != exitUsage {
			t.Errorf("%s: exit %d, want %d", name, code, exitUsage)
		}
	}
	if code := runRegistryPublisher(append(append([]string(nil), base...), "-token", "0123456789abcdef0123456789abcdef")); code != exitUsage {
		t.Errorf("a -token flag was accepted (exit %d)", code)
	}
}
