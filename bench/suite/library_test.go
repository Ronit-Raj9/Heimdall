package suite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSuiteYAML(t *testing.T, dir string, tasks int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, tasks)
	for i := range ids {
		ids[i] = fmt.Sprintf("t%d", i)
	}
	body := "id: " + filepath.Base(dir) + "\ntasks: [" + strings.Join(ids, ", ") + "]\n"
	if err := os.WriteFile(filepath.Join(dir, SuiteFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The library wins a shared id unless the org's copy lists more tasks.
func TestPickSuiteDir(t *testing.T) {
	org, lib := t.TempDir(), t.TempDir()
	writeSuiteYAML(t, filepath.Join(lib, "tb2"), 89)
	writeSuiteYAML(t, filepath.Join(org, "tb2"), 3)
	writeSuiteYAML(t, filepath.Join(lib, "big"), 2)
	writeSuiteYAML(t, filepath.Join(org, "big"), 5)
	writeSuiteYAML(t, filepath.Join(lib, "same"), 4)
	writeSuiteYAML(t, filepath.Join(org, "same"), 4)
	writeSuiteYAML(t, filepath.Join(org, "mine"), 1)
	cases := []struct {
		id      string
		wantLib bool
	}{{"tb2", true}, {"big", false}, {"same", true}, {"mine", false}, {"absent", false}, {"../x", false}}
	for _, c := range cases {
		dir, fromLib := PickSuiteDir(org, lib, c.id)
		if fromLib != c.wantLib {
			t.Errorf("%s: fromLibrary=%v want %v (%s)", c.id, fromLib, c.wantLib, dir)
		}
	}
	if _, fromLib := PickSuiteDir(org, "", "tb2"); fromLib {
		t.Error("no library still picked the library")
	}
}

// A layered loader loads a library-only pack by id with its library Root.
func TestLayeredLoaderLoadsLibrarySuite(t *testing.T) {
	org, lib := t.TempDir(), t.TempDir()
	src := filepath.Join("testdata", "valid", "mini")
	if err := os.CopyFS(filepath.Join(lib, "mini"), os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	s, err := Layered(org, lib)[0].Load(context.Background(), "mini")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Root, lib) {
		t.Fatalf("Root = %s, want under the library %s", s.Root, lib)
	}
	if _, err := Builtin(org)[0].Load(context.Background(), "mini"); err == nil {
		t.Fatal("plain loader found the library pack")
	}
}

// With LibraryEnv set, every plain loader (LoadTask, Builtin) reads a
// library suite: the task page, health and judge all go through these.
func TestLoadTaskFromLibrary(t *testing.T) {
	org, lib := t.TempDir(), t.TempDir()
	if err := os.CopyFS(filepath.Join(lib, "benchmarks", "mini"), os.DirFS(filepath.Join("testdata", "valid", "mini"))); err != nil {
		t.Fatal(err)
	}
	t.Setenv(LibraryEnv, lib)
	s, task, err := LoadTask(org, "mini", "m01")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Root, lib) || !strings.HasPrefix(task.Dir, lib) {
		t.Fatalf("root %s task %s: want under the library", s.Root, task.Dir)
	}
	if _, err := Builtin(org)[0].Load(context.Background(), "mini"); err != nil {
		t.Fatalf("Builtin loader misses the library: %v", err)
	}
	if !IsLibrarySuite(org, "mini") || IsLibrarySuite(org, "other") {
		t.Fatal("IsLibrarySuite wrong")
	}
}
