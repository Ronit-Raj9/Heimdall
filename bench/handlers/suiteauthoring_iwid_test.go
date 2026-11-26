package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFilePathEscapedDotsStayInside: the file route unescapes the path
// (p11-1), so an escaped "../" must still be refused and nothing may be
// written outside the benchmark's own folder, nor into the built-in id.
func TestFilePathEscapedDotsStayInside(t *testing.T) {
	c := newStampBuildClient(t)
	createBlankQA(t, c, "own-a")
	createBlankQA(t, c, "own-b")
	for _, p := range []string{
		"%2e%2e%2fown-b%2fplanted.txt",
		"..%2Fown-b%2Fplanted.txt",
		"sub%2f..%2f..%2fown-b%2fplanted.txt",
		"%2e%2e%2f%2e%2e%2fplanted.txt",
		"x%00.txt",
	} {
		code, body := c.do(t, "PUT", "/suites/own-a/files/"+p, []byte("x\n"), nil)
		if code/100 == 2 {
			t.Fatalf("PUT %s: want a refusal, got %d %s", p, code, body)
		}
	}
	// A leading slash is trimmed: the file lands inside the benchmark, never at the root.
	if code, body := c.do(t, "PUT", "/suites/own-a/files/%2fetc%2finside.txt", []byte("x\n"), nil); code/100 != 2 {
		t.Fatalf("PUT a leading slash: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "own-a", "etc", "inside.txt")); err != nil {
		t.Fatalf("the trimmed path is not inside the benchmark: %v", err)
	}
	err := filepath.Walk(c.root, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasPrefix(info.Name(), "planted") {
			t.Errorf("a file was written outside its benchmark folder: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// A new benchmark cannot take a built-in id.
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"coding-core","template":"qa","blank":true}`), nil); code != 409 {
		t.Fatalf("create coding-core: want 409, got %d %s", code, body)
	}
}
