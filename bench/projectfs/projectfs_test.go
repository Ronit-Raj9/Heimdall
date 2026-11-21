package projectfs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResolveRejectsTraversal(t *testing.T) {
	s := newStore(t)
	for _, rel := range []string{"../x", "a/../../x", "/etc/passwd", ".git/config", "a/.heimdall/x", "..\\x", "a\x00b"} {
		if _, err := s.Resolve(AreaAgents, rel); !errors.Is(err, ErrOutside) {
			t.Errorf("Resolve(%q) = %v, want ErrOutside", rel, err)
		}
	}
	if _, err := s.Resolve(Area("secrets"), "x"); !errors.Is(err, ErrOutside) {
		t.Errorf("unknown area: %v", err)
	}
	// A symlink out of the area is refused.
	out := t.TempDir()
	if err := os.MkdirAll(filepath.Join(s.Root(), "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(s.Root(), "agents", "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(context.Background(), AreaAgents, "link/x.txt", []byte("x"), WriteOpts{}); !errors.Is(err, ErrOutside) {
		t.Errorf("symlink escape write = %v, want ErrOutside", err)
	}
}

func TestWriteVersionsAndIfMatch(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	e1, err := s.Write(ctx, AreaUploads, "notes/a.txt", []byte("one"), WriteOpts{Actor: "ann"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Write(ctx, AreaUploads, "notes/a.txt", []byte("two"), WriteOpts{IfMatch: "deadbeef"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale If-Match = %v, want ErrConflict", err)
	}
	if _, err := s.Write(ctx, AreaUploads, "notes/a.txt", []byte("two"), WriteOpts{IfMatch: e1.SHA256, Note: "edit"}); err != nil {
		t.Fatal(err)
	}
	vs, err := s.Versions(ctx, AreaUploads, "notes/a.txt")
	if err != nil || len(vs) != 2 {
		t.Fatalf("versions = %v, %v; want 2", vs, err)
	}
	if vs[0].Note != "edit" || vs[1].Actor != "ann" {
		t.Errorf("versions not newest first: %+v", vs)
	}
	old, err := s.ReadVersion(ctx, AreaUploads, "notes/a.txt", vs[1].ID)
	if err != nil || string(old) != "one" {
		t.Errorf("ReadVersion = %q, %v", old, err)
	}
	if _, err := s.Write(ctx, AreaAgents, "big.bin", make([]byte, MaxFileBytes+1), WriteOpts{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big write = %v", err)
	}
	if err := s.Delete(ctx, AreaUploads, "notes/a.txt", WriteOpts{}); err != nil {
		t.Fatal(err)
	}
	vs, _ = s.Versions(ctx, AreaUploads, "notes/a.txt")
	if len(vs) != 3 || vs[0].Note != "deleted" {
		t.Errorf("delete not recorded: %+v", vs)
	}
	// The version store is hidden from the area listings.
	ents, _ := s.List(ctx, AreaUploads, "", true)
	for _, e := range ents {
		if filepath.Base(e.Path) == "index.jsonl" {
			t.Errorf("listing shows %s", e.Path)
		}
	}
}

func TestValidators(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, err := s.Write(ctx, AreaAgents, "bad.yaml", []byte("id: x\n"), WriteOpts{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("agent without adapter = %v, want ErrInvalid", err)
	}
	if _, err := s.Write(ctx, AreaUploads, "x.yaml", []byte("a: [\n"), WriteOpts{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("broken yaml = %v, want ErrInvalid", err)
	}
	if _, err := s.Write(ctx, AreaPolicies, "schedules.yaml", []byte("schedules: []\n"), WriteOpts{}); err != nil {
		t.Errorf("non-policy yaml in policies/ = %v", err)
	}
	if _, err := s.Write(ctx, AreaPolicies, "p.yaml", []byte("policy_version: 1\n"), WriteOpts{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("incomplete policy = %v, want ErrInvalid", err)
	}
	raw, err := os.ReadFile("../examples/acme-helpdesk/agents/noop.yaml")
	if err != nil {
		t.Skip(err)
	}
	if _, err := s.Write(ctx, AreaAgents, "noop.yaml", raw, WriteOpts{}); err != nil {
		t.Errorf("example agent refused: %v", err)
	}
}

func zipOf(t *testing.T, files map[string]string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return bytes.NewReader(buf.Bytes())
}

func TestExtractZip(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r := zipOf(t, map[string]string{"skills/a.md": "hi", "skills/b/c.txt": "x", "__MACOSX/._a": "junk"})
	ents, err := s.ExtractZip(ctx, AreaAgents, "bot", r, r.Size(), WriteOpts{})
	if err != nil || len(ents) != 2 {
		t.Fatalf("extract = %v, %v", ents, err)
	}
	if b, _, err := s.Read(ctx, AreaAgents, "bot/skills/b/c.txt"); err != nil || string(b) != "x" {
		t.Errorf("read back = %q, %v", b, err)
	}
	for _, bad := range []string{"../evil.txt", "a/../../evil.txt", "/abs.txt", ".git/hooks/x", "C:/x"} {
		r := zipOf(t, map[string]string{"ok.txt": "ok", bad: "evil"})
		if _, err := s.ExtractZip(ctx, AreaUploads, "z", r, r.Size(), WriteOpts{}); err == nil {
			t.Errorf("zip with %q accepted", bad)
		}
		if _, _, err := s.Read(ctx, AreaUploads, "z/ok.txt"); !errors.Is(err, ErrNotFound) {
			t.Errorf("zip with %q wrote files before refusing", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Root()), "evil.txt")); err == nil {
		t.Error("zip slip wrote outside the project")
	}
}

func TestUploadsAreaLargeFileCap(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	big := make([]byte, 2<<20)
	if _, err := s.Write(ctx, AreaUploads, "datasets/x/rows.jsonl", big, WriteOpts{}); err != nil {
		t.Fatalf("uploads 2 MiB: %v", err)
	}
	if _, _, err := s.Read(ctx, AreaUploads, "datasets/x/rows.jsonl"); err != nil {
		t.Fatalf("read uploads 2 MiB: %v", err)
	}
	if _, err := s.Write(ctx, AreaAgents, "big.txt", big, WriteOpts{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("agents 2 MiB: %v, want too large", err)
	}
	if _, err := s.Write(ctx, AreaUploads, "datasets/x/huge", make([]byte, MaxUploadFileBytes+1), WriteOpts{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("uploads over cap: %v", err)
	}
}
