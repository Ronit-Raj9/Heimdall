package builder

import (
	"context"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The shared worker runs with umask 077 and docker cp keeps modes: the files
// copied into a build container must still be 0644 in 0755 folders, or a
// non-root user there (and /etc itself) loses them. Found in the first real
// hosted run (the egress CA was 0600).
func TestCopyFilesModesIgnoreUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	modes := map[string]fs.FileMode{}
	run := func(_ context.Context, _ []byte, args ...string) ([]byte, error) {
		if args[0] == "cp" {
			src := args[1]
			filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err == nil {
					if fi, err := d.Info(); err == nil {
						rel, _ := filepath.Rel(filepath.Dir(src), p)
						modes[rel] = fi.Mode().Perm()
					}
				}
				return nil
			})
		}
		return nil, nil
	}
	s := &dockerSandbox{id: "c1", cfg: DockerConfig{Run: run, Files: map[string][]byte{"/etc/heimdall-egress/ca.crt": []byte("CERT\n")}}}
	if err := s.copyFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]fs.FileMode{"etc": 0o755, "etc/heimdall-egress": 0o755, "etc/heimdall-egress/ca.crt": 0o644}
	for p, m := range want {
		if modes[p] != m {
			t.Fatalf("%s copied with mode %o, want %o (all: %v)", p, modes[p], m, modes)
		}
	}
}

// A state overlay of a 97 KB CHANGELOG made the exec argv pass Linux's 128
// KiB per-argument limit, so every run_tests failed (marshmallow #2936, real
// hosted run). The command must reach the container on stdin.
func TestExecSendsCommandOnStdin(t *testing.T) {
	big := strings.Repeat("x", 300<<10)
	var gotArgs []string
	var gotStdin []byte
	run := func(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
		gotArgs, gotStdin = args, stdin
		return nil, nil
	}
	s := &dockerSandbox{id: "c1", cfg: DockerConfig{Run: run}}
	if _, _, err := s.exec(context.Background(), "echo "+big, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, a := range gotArgs {
		if len(a) > 4096 {
			t.Fatalf("an argv string of %d bytes", len(a))
		}
	}
	if string(gotStdin) != "echo "+big || !slices.Contains(gotArgs, "-i") {
		t.Fatalf("stdin %d bytes, args %v", len(gotStdin), gotArgs)
	}
}
