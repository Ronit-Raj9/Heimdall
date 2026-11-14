package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestVerifyRunFilesCache(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	r, rec := sealedRun(t, s, m, "s_a1", "r_1")
	dir := s.runDir(r)
	sentinel := errors.New("served from the cache")

	if err := s.verifyRunFiles(dir, rec); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	e, ok := s.verified.entries[dir]
	if !ok || e.err != nil || e.sealHash != rec.Hash {
		t.Fatalf("cache entry %+v %v, want a clean result for the seal", e, ok)
	}
	// An unchanged folder is answered from the cache: plant a marker result.
	e.err = sentinel
	s.verified.put(dir, e)
	if err := s.verifyRunFiles(dir, rec); !errors.Is(err, sentinel) {
		t.Fatalf("unchanged folder: err = %v, want the cached result", err)
	}
	// A different seal hash never reuses the entry.
	other := *rec
	other.Hash = strings.Repeat("0", 64)
	if err := s.verifyRunFiles(dir, &other); err != nil {
		t.Fatalf("other seal hash: %v, want a fresh comparison", err)
	}

	// Rewrite one byte, keeping size and modification time: the change time
	// moves, so the comparison runs again. Only Linux reads the change time;
	// elsewhere the cache relies on size, modification time and mode.
	if runtime.GOOS != "linux" {
		return
	}
	makeWritable(t, dir)
	p := filepath.Join(dir, "events.jsonl")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.verifyRunFiles(dir, rec); err != nil {
		t.Fatalf("after chmod: %v", err)
	}
	b[0] ^= 1
	writeTampered(t, p, b)
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	err = s.verifyRunFiles(dir, rec)
	if !errors.Is(err, schemas.ErrSealMismatch) || !strings.Contains(err.Error(), "file events.jsonl changed") {
		t.Fatalf("rewritten file: err = %v, want ErrSealMismatch naming events.jsonl", err)
	}
	// The mismatch is cached and served again for the same bytes.
	if err := s.verifyRunFiles(dir, rec); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("second verify of a tampered folder: err = %v", err)
	}
}

func TestStampRunFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"run.json", "evidence/shim.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := stampRunFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got["run.json"].size != int64(len("run.json")) || got["evidence/shim.log"].mtime == 0 {
		t.Fatalf("stamps %+v", got)
	}
	before := got["run.json"]
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("run.jsoN"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := stampRunFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after["run.json"] == before {
		t.Fatal("a rewrite of the same size left the stamp unchanged")
	}
	if _, err := stampRunFiles(filepath.Join(dir, "none")); err == nil {
		t.Fatal("a missing folder stamped")
	}
}
