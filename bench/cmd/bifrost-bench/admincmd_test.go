package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAdminGrantCLI: the bootstrap path for a server admin by user id, and
// user verify for servers that send no email (Wave A, audit B1).
func TestAdminGrantCLI(t *testing.T) {
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
	if code := adminMain([]string{"list", "-config", cfgPath}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "no server admins") {
		t.Fatalf("empty list = %d %q %q", code, out.String(), errb.String())
	}
	if code := userMain([]string{"create", "-config", cfgPath, "-email", "root@example.com", "-org", "Acme"}, strings.NewReader("violet-tractor-umbrella\n"), &out, &errb); code != exitOK {
		t.Fatalf("create = %d %q", code, errb.String())
	}
	out.Reset()
	if code := adminMain([]string{"grant", "-config", cfgPath, "-email", "Root@Example.com"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "server admin by user id") {
		t.Fatalf("grant = %d %q %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := adminMain([]string{"list", "-config", cfgPath}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "root@example.com") ||
		!strings.Contains(out.String(), "granted") || !strings.Contains(out.String(), "\tverified") {
		t.Fatalf("list = %d %q", code, out.String())
	}
	out.Reset()
	if code := adminMain([]string{"revoke", "-config", cfgPath, "-email", "root@example.com"}, &out, &errb); code != exitOK || !strings.Contains(out.String(), "revoked") {
		t.Fatalf("revoke = %d %q", code, out.String())
	}
	out.Reset()
	if code := userMain([]string{"verify", "-config", cfgPath, "-email", "root@example.com"}, strings.NewReader(""), &out, &errb); code != exitOK || !strings.Contains(out.String(), "verified") {
		t.Fatalf("verify = %d %q %q", code, out.String(), errb.String())
	}
	for _, args := range [][]string{{"grant", "-config", cfgPath}, {"promote"}, {}} {
		if code := adminMain(args, &out, &errb); code != exitUsage {
			t.Errorf("%v = %d, want usage", args, code)
		}
	}
	if code := adminMain([]string{"grant", "-config", cfgPath, "-email", "nobody@example.com"}, &out, &errb); code == exitOK {
		t.Fatal("grant for a missing account succeeded")
	}
}
