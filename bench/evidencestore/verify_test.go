package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

func TestVerifyReport(t *testing.T) {
	ctx := context.Background()
	org := newOrg(t, "local")
	f := standard(t, org, "e_ver")
	mir, d := newDirMirror(t)
	if _, err := mir.Experiment(ctx, org, "e_ver"); err != nil {
		t.Fatal(err)
	}
	rep, err := mir.Verify(ctx, org, "e_ver", true)
	if err != nil || !rep.OK() || len(rep.Extra)+len(rep.Unlisted) != 0 || len(rep.Pending) != 1 {
		t.Fatalf("clean verify: %+v %v", rep, err)
	}
	// Missing: a run sealed after the pass. Extra: a local analysis file
	// removed. Mismatched: a mirror object changed. Unlisted: a stray
	// object.
	f.seal("r_4")
	makeWritable(filepath.Join(f.dir(), "analysis"))
	if err := os.Remove(filepath.Join(f.dir(), "analysis", "a_1", "report.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Root(), "local", "e_ver", "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Root(), "local", "e_ver", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, deep := range []bool{false, true} {
		rep, err = mir.Verify(ctx, org, "e_ver", deep)
		if err != nil {
			t.Fatal(err)
		}
		if rep.OK() {
			t.Fatal("verify passed a broken mirror")
		}
		joined := func(s []string) string { return strings.Join(s, "|") }
		if !strings.Contains(joined(rep.Missing), "r_4/seal.json") || !strings.Contains(joined(rep.Missing), "seals.log: new lines") {
			t.Errorf("deep=%v missing: %v", deep, rep.Missing)
		}
		if joined(rep.Extra) != "analysis/a_1/report.md" {
			t.Errorf("deep=%v extra: %v", deep, rep.Extra)
		}
		if !strings.Contains(joined(rep.Mismatched), "manifest.json") {
			t.Errorf("deep=%v mismatched: %v", deep, rep.Mismatched)
		}
		if joined(rep.Unlisted) != "stray.txt" {
			t.Errorf("deep=%v unlisted: %v", deep, rep.Unlisted)
		}
	}
}

func TestVerifyEdgeCases(t *testing.T) {
	ctx := context.Background()
	t.Run("object gone and local file gone", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_gone")
		f.sealed("s_1", "r_1")
		f.analysis("a_1")
		mir, d := newDirMirror(t)
		if _, err := mir.Experiment(ctx, org, "e_gone"); err != nil {
			t.Fatal(err)
		}
		makeWritable(filepath.Join(f.dir(), "analysis"))
		if err := os.Remove(filepath.Join(f.dir(), "analysis", "a_1", "report.md")); err != nil {
			t.Fatal(err)
		}
		if err := d.Delete(ctx, "local/e_gone/analysis/a_1/report.md"); err != nil {
			t.Fatal(err)
		}
		rep, err := mir.Verify(ctx, org, "e_gone", false)
		if err != nil || rep.OK() || !strings.Contains(strings.Join(rep.Missing, " "), "report.md: listed but the object is gone") {
			t.Fatalf("verify: %+v %v", rep, err)
		}
		if _, err := mir.Archive(ctx, org, "e_gone"); !errors.Is(err, ErrRefused) {
			t.Fatalf("archive with a lost object: %v", err)
		}
		if !isLive(f.dir()) {
			t.Fatal("refused archive deleted the folder")
		}
	})
	t.Run("mirror only", func(t *testing.T) {
		org := newOrg(t, "local")
		f := newExperiment(t, org, "e_only")
		f.sealed("s_1", "r_1")
		mir, _ := newDirMirror(t)
		if _, err := mir.Experiment(ctx, org, "e_only"); err != nil {
			t.Fatal(err)
		}
		makeWritable(f.dir())
		if err := os.RemoveAll(f.dir()); err != nil {
			t.Fatal(err)
		}
		rep, err := mir.Verify(ctx, org, "e_only", false)
		if err != nil || rep.OK() || !strings.Contains(rep.Local, "no local folder") || len(rep.Extra) == 0 {
			t.Fatalf("verify mirror-only: %+v %v", rep, err)
		}
	})
	t.Run("log object longer than the listing", func(t *testing.T) {
		// A later pass sent a longer seals.log and failed before its
		// listing: verify and restore still read the listed prefix.
		org := newOrg(t, "local")
		f := standard(t, org, "e_longlog")
		mir, d := newDirMirror(t)
		if _, err := mir.Experiment(ctx, org, "e_longlog"); err != nil {
			t.Fatal(err)
		}
		f.seal("r_4")
		b, _ := readLogSnapshot(filepath.Join(f.dir(), sealsLogFile))
		if err := d.Put(ctx, "local/e_longlog/seals.log", PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
			t.Fatal(err)
		}
		for _, deep := range []bool{false, true} {
			rep, err := mir.Verify(ctx, org, "e_longlog", deep)
			if err != nil || len(rep.Mismatched) != 0 {
				t.Fatalf("deep=%v verify: %+v %v", deep, rep, err)
			}
		}
		target := t.TempDir()
		t.Cleanup(func() { makeWritable(target) })
		if _, err := mir.Restore(ctx, org, "e_longlog", RestoreOptions{Target: target}); err != nil {
			t.Fatalf("restore: %v", err)
		}
	})
}
