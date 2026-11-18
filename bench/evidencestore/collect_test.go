package evidencestore

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotRefusesBrokenChain(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_chain")
	mir, _ := newDirMirror(t)
	// Remove a line from seals.log: the chain no longer covers r_2.
	log := filepath.Join(f.dir(), sealsLogFile)
	b, _ := os.ReadFile(log)
	lines := strings.SplitAfter(string(b), "\n")
	if err := os.WriteFile(log, []byte(lines[0]+lines[2]), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.Experiment(ctx, org, "e_chain"); !errors.Is(err, ErrRefused) {
		t.Fatalf("broken chain: %v", err)
	}
}

// TestRecordedExperimentRoundTrip mirrors and restores a real recorded
// experiment (testdata/recorded), so the manifest and seal checks are held
// against what the engine actually writes.
func TestRecordedExperimentRoundTrip(t *testing.T) {
	src := filepath.Join("..", "testdata", "recorded", "e_smoke_sdoj")
	if _, err := os.Stat(src); err != nil {
		t.Skip("recorded experiment not present")
	}
	ctx := context.Background()
	org := newOrg(t, "local")
	if err := copyTree(src, filepath.Join(org.root(), "e_smoke_sdoj")); err != nil {
		t.Fatal(err)
	}
	mir, _ := newDirMirror(t)
	res, err := mir.Experiment(ctx, org, "e_smoke_sdoj")
	if err != nil {
		t.Fatal(err)
	}
	if res.Seals == 0 || len(res.Pending) != 0 {
		t.Fatalf("recorded: %+v", res)
	}
	target := t.TempDir()
	t.Cleanup(func() { makeWritable(target) })
	if _, err := mir.Restore(ctx, org, "e_smoke_sdoj", RestoreOptions{Target: target}); err != nil {
		t.Fatal(err)
	}
	if rep, err := mir.Verify(ctx, org, "e_smoke_sdoj", true); err != nil || !rep.OK() {
		t.Fatalf("verify: %+v %v", rep, err)
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// TestDotFileInSealedRun checks that a sealed run's own dot files are
// mirrored and restored: only dot names outside sealed runs (temporary
// files, state folders) are left out.
func TestDotFileInSealedRun(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := newExperiment(t, org, "e_dot")
	f.sealed("s_1", "r_1", ".agentrc", "x=1\n", "evidence/.trace", "t\n")
	// A temporary file the store left at the experiment level is skipped.
	if err := os.WriteFile(filepath.Join(f.dir(), ".manifest.json.tmp-1"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	mir, d := newDirMirror(t)
	if _, err := mir.Experiment(ctx, org, "e_dot"); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(d.Root(), "local", "e_dot")
	if _, err := os.Stat(filepath.Join(base, "slots", "s_1", "runs", "r_1", ".agentrc")); err != nil {
		t.Fatal("dot file of a sealed run not mirrored")
	}
	if _, err := os.Stat(filepath.Join(base, ".manifest.json.tmp-1")); err == nil {
		t.Fatal("temporary file mirrored")
	}
	target := t.TempDir()
	t.Cleanup(func() { makeWritable(target) })
	if _, err := mir.Restore(ctx, org, "e_dot", RestoreOptions{Target: target}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(target, "e_dot", "slots", "s_1", "runs", "r_1", "evidence", ".trace")); err != nil || string(b) != "t\n" {
		t.Fatalf("restored dot file: %q %v", b, err)
	}
}

func TestSnapshotRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("file directly under runs", func(t *testing.T) {
		org := newOrg(t, "local")
		f := standard(t, org, "e_junk")
		if err := os.WriteFile(filepath.Join(f.dir(), "slots", "s_2", "runs", "junk"), []byte("x"), 0o644); err != nil {
			// The slot's runs folder is writable; only run folders are sealed.
			t.Fatal(err)
		}
		mir, _ := newDirMirror(t)
		if _, err := mir.Experiment(ctx, org, "e_junk"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "not a run folder") {
			t.Fatalf("stray file: %v", err)
		}
	})
	t.Run("sealed name too long for a key", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_long")
		seg := strings.Repeat("d", 200)
		name := strings.Join([]string{seg, seg, seg, seg, seg, "f.txt"}, "/")
		f.sealed("s_1", "r_1", name, "x")
		mir, _ := newDirMirror(t)
		if _, err := mir.Experiment(ctx, org, "e_long"); !errors.Is(err, ErrRefused) {
			t.Fatalf("long name: %v", err)
		}
	})
}

// TestResumesLogIsAppendOnly: resumes.jsonl grows with every allowed drift
// on resume; growth is mirrored, a rewrite is refused, a torn last line is
// left for the next pass.
func TestResumesLogIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_resumes")
	p := filepath.Join(f.dir(), "resumes.jsonl")
	if err := os.WriteFile(p, []byte(`{"n":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mir, d := newDirMirror(t)
	if _, err := mir.Experiment(ctx, org, "e_resumes"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"n":1}`+"\n"+`{"n":2}`+"\n"+`{"n":3,"torn`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := mir.Experiment(ctx, org, "e_resumes")
	if err != nil || res.Uploaded != 1 {
		t.Fatalf("grown resumes.jsonl: %+v %v", res, err)
	}
	b, _ := os.ReadFile(filepath.Join(d.Root(), "local", "e_resumes", "resumes.jsonl"))
	if string(b) != `{"n":1}`+"\n"+`{"n":2}`+"\n" {
		t.Fatalf("mirrored log %q: torn line sent", b)
	}
	if err := os.WriteFile(p, []byte(`{"n":9}`+"\n"+`{"n":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.Experiment(ctx, org, "e_resumes"); !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "does not extend") {
		t.Fatalf("rewritten resumes.jsonl: %v", err)
	}
}
