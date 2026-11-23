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

// Scan records (agents/<id>/skill-scans/) admit an uploaded skill to runs,
// so the store refuses every write, delete and zip entry there unless the
// caller is Heimdall's own scan (WriteOpts.System). Fix wave 3, SEC-B.

func reservedZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStoreRefusesScanRecordWrites(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	for _, rel := range []string{"zz/skill-scans/evil.json", "zz/skill-scans/deep/x.json", "./zz//skill-scans/evil.json", "zz/skill-scans"} {
		if _, err := s.Write(ctx, AreaAgents, rel, []byte("{}"), WriteOpts{}); !errors.Is(err, ErrReserved) {
			t.Errorf("Write(%q) = %v, want ErrReserved", rel, err)
		}
	}
	if _, err := s.(*store).WriteLarge(ctx, AreaAgents, "zz/skill-scans/evil.json", []byte("{}"), WriteOpts{}); !errors.Is(err, ErrReserved) {
		t.Errorf("WriteLarge = %v, want ErrReserved", err)
	}
	// Heimdall's own scan writes the record.
	if _, err := s.Write(ctx, AreaAgents, "zz/skill-scans/good.json", []byte("{}"), WriteOpts{System: true}); err != nil {
		t.Fatalf("system write: %v", err)
	}
	// Other folders and other areas are not reserved.
	if _, err := s.Write(ctx, AreaAgents, "zz/skills/good/SKILL.md", []byte("# good\n"), WriteOpts{}); err != nil {
		t.Fatalf("skill write: %v", err)
	}
	if _, err := s.Write(ctx, AreaBenchmarks, "zz/skill-scans/x.json", []byte("{}"), WriteOpts{}); err != nil {
		t.Fatalf("benchmarks write: %v", err)
	}
}

func TestStoreRefusesScanRecordDeletes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.Write(ctx, AreaAgents, "zz/skill-scans/good.json", []byte("{}"), WriteOpts{System: true}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"zz/skill-scans/good.json", "zz/skill-scans"} {
		if err := s.Delete(ctx, AreaAgents, rel, WriteOpts{}); !errors.Is(err, ErrReserved) {
			t.Errorf("Delete(%q) = %v, want ErrReserved", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "agents", "zz", "skill-scans", "good.json")); err != nil {
		t.Fatalf("record gone after refused deletes: %v", err)
	}
	if err := s.Delete(ctx, AreaAgents, "zz/skill-scans/good.json", WriteOpts{System: true}); err != nil {
		t.Fatalf("system delete: %v", err)
	}
	// Deleting the whole agent folder stays allowed: records go with their skills.
	if _, err := s.Write(ctx, AreaAgents, "zz/skill-scans/good.json", []byte("{}"), WriteOpts{System: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, AreaAgents, "zz", WriteOpts{}); err != nil {
		t.Fatalf("delete agent folder: %v", err)
	}
}

func TestExtractZipRefusesScanRecordEntry(t *testing.T) {
	s := newStore(t)
	if err := os.MkdirAll(filepath.Join(s.Root(), "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := reservedZip(t, map[string]string{"skills/evil/SKILL.md": "# evil\n", "skill-scans/evil.json": `{"decision":"admitted"}`})
	_, err := s.ExtractZip(context.Background(), AreaAgents, "zz", bytes.NewReader(data), int64(len(data)), WriteOpts{})
	if !errors.Is(err, ErrReserved) {
		t.Fatalf("ExtractZip = %v, want ErrReserved", err)
	}
	for _, rel := range []string{"zz/skill-scans/evil.json", "zz/skills/evil/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(s.Root(), "agents", filepath.FromSlash(rel))); err == nil {
			t.Fatalf("%s was written by a refused archive", rel)
		}
	}
	// The same archive unpacked into the agents area root ("zz/skill-scans/...").
	data = reservedZip(t, map[string]string{"zz/skill-scans/evil.json": "{}"})
	if _, err := s.ExtractZip(context.Background(), AreaAgents, "", bytes.NewReader(data), int64(len(data)), WriteOpts{}); !errors.Is(err, ErrReserved) {
		t.Fatalf("ExtractZip at the area root = %v, want ErrReserved", err)
	}
}

func TestIsReserved(t *testing.T) {
	cases := map[string]bool{
		"zz/skill-scans/x.json": true, "zz/skill-scans": true, "zz/skill-scans/a/b": true,
		"zz": false, "zz/skills/x/SKILL.md": false, "skill-scans/x.json": false, "zz/x/skill-scans/y": false,
	}
	for rel, want := range cases {
		if got := IsReserved(AreaAgents, rel); got != want {
			t.Errorf("IsReserved(agents, %q) = %v, want %v", rel, got, want)
		}
	}
	if IsReserved(AreaBenchmarks, "zz/skill-scans/x.json") {
		t.Error("benchmarks area reserved")
	}
}
