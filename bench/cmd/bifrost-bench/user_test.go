package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserCreateCLI(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "deploy", "bench.yaml")
	cfg := "runs_root: runs\nserver:\n  listen: 127.0.0.1:0\n  mode: live\nauth:\n  mode: hosted\n  public_url: https://heimdall.example\n  signup: closed\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := userMain([]string{"create", "-config", cfgPath, "-email", "root@example.com", "-org", "Acme"}, strings.NewReader("violet-tractor-umbrella\n"), &out, &errb)
	if code != exitOK || !strings.Contains(out.String(), "root@example.com") || !strings.Contains(out.String(), "created, role owner") {
		t.Fatalf("create = %d, out %q, err %q", code, out.String(), errb.String())
	}
	out.Reset()
	code = userMain([]string{"set-password", "-config", cfgPath, "-email", "root@example.com"}, strings.NewReader("orange-bicycle-lantern\r\n"), &out, &errb)
	if code != exitOK || !strings.Contains(out.String(), "session") {
		t.Fatalf("set-password = %d, out %q", code, out.String())
	}
	// Usage errors: no password on stdin, bad role, missing flags.
	for _, args := range [][]string{
		{"create", "-config", cfgPath, "-email", "x@example.com", "-org", "Acme", "-role", "root"},
		{"create", "-config", cfgPath, "-email", "x@example.com"},
		{"delete"},
	} {
		if code := userMain(args, strings.NewReader("violet-tractor-umbrella\n"), &out, &errb); code != exitUsage {
			t.Errorf("%v = %d, want usage", args, code)
		}
	}
	if code := userMain([]string{"create", "-config", cfgPath, "-email", "y@example.com", "-org", "Acme"}, strings.NewReader(""), &out, &errb); code != exitUsage {
		t.Errorf("empty stdin = %d, want usage", code)
	}
}
