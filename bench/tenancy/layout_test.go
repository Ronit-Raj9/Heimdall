package tenancy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedCopy copies bench/testdata/recorded into a temp folder: the
// originals are never written.
func recordedCopy(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "recorded")
	if err := os.CopyFS(dst, os.DirFS(filepath.Join("..", "testdata", "recorded"))); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestHostedPathsStayInStorage(t *testing.T) {
	root := t.TempDir()
	l := Layout{Hosted: true, StorageRoot: root}
	a, err := l.Paths("org_a")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := l.Paths("org_b")
	if a.StoreRoot != filepath.Join(root, "orgs", "org_a") || a.ProjectRoot != filepath.Join(root, "projects", "org_a") {
		t.Fatalf("paths %+v", a)
	}
	for _, p := range []string{a.StoreRoot, a.ProjectRoot, a.ReleasesRoot} {
		if strings.HasPrefix(p, b.StoreRoot) || strings.HasPrefix(p, b.ProjectRoot) {
			t.Fatalf("org a path %s inside org b", p)
		}
	}
	if within(a.StoreRoot, a.ReleasesRoot) || within(a.StoreRoot, a.ProjectRoot) {
		t.Fatal("project or releases inside the evidence store")
	}
	if _, err := l.Paths(LocalOrgID); err != nil {
		t.Fatalf("hosted local org: %v", err) // a valid id like any other
	}
}

func TestEnsureProjectSeedsCodingCore(t *testing.T) {
	root := t.TempDir()
	p, _ := Layout{Hosted: true, StorageRoot: root}.Paths("org_a")
	if err := EnsureProject(p, filepath.Join("..", "benchmarks")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.BenchmarksRoot(), DefaultSuite, "suite.yaml")); err != nil {
		t.Fatalf("coding-core not seeded: %v", err)
	}
	for _, d := range []string{AgentsDir, ExperimentsDir, PoliciesDir, ReleasesDir} {
		if fi, err := os.Stat(filepath.Join(p.ProjectRoot, d)); err != nil || !fi.IsDir() {
			t.Fatalf("%s missing", d)
		}
	}
	// An org's edit survives a second call.
	mark := filepath.Join(p.BenchmarksRoot(), DefaultSuite, "suite.yaml")
	if err := os.WriteFile(mark, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProject(p, filepath.Join("..", "benchmarks")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(mark); string(b) != "edited" {
		t.Fatal("seed overwrote the org's suite")
	}
	// Folders first (an org made outside tenancy), seed later: seeded once.
	q, _ := Layout{Hosted: true, StorageRoot: root}.Paths("org_b")
	if err := EnsureProject(q, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q.BenchmarksRoot(), DefaultSuite)); !os.IsNotExist(err) {
		t.Fatal("seeded without a seed folder")
	}
	if err := EnsureProject(q, filepath.Join("..", "benchmarks")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q.BenchmarksRoot(), DefaultSuite, "suite.yaml")); err != nil {
		t.Fatalf("late seed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(q.BenchmarksRoot(), DefaultSuite)); err != nil {
		t.Fatal(err)
	}
	if err := EnsureProject(q, filepath.Join("..", "benchmarks")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(q.BenchmarksRoot(), DefaultSuite)); !os.IsNotExist(err) {
		t.Fatal("a suite the org deleted was seeded again")
	}
}

func TestCopyTreeRefusesSymlinks(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "a"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := CopyTree(src, dst); err == nil {
		t.Fatal("symlink copied")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("failed copy left the destination behind")
	}
}

func TestImportRunsVerifiesAndCopies(t *testing.T) {
	src := recordedCopy(t)
	dst := filepath.Join(t.TempDir(), "orgs", "org_demo")
	rep, err := ImportRuns(context.Background(), src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Imported != 2 || rep.Refused != 0 || rep.Runs == 0 {
		t.Fatalf("report %+v", rep)
	}
	// The import verifies on the copy: the copy verifies again here.
	for _, e := range rep.Experiments {
		if n, err := VerifyExperiment(context.Background(), dst, e.ExpID, nil); err != nil || n != e.Runs {
			t.Fatalf("%s: %d runs, %v", e.ExpID, n, err)
		}
	}
	// No staging folder is left behind.
	entries, _ := os.ReadDir(dst)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), importStaging) {
			t.Fatalf("staging folder left: %s", e.Name())
		}
	}
	// A second import skips what is there.
	rep2, err := ImportRuns(context.Background(), src, dst, nil)
	if err != nil || rep2.Skipped != 2 || rep2.Imported != 0 {
		t.Fatalf("second import %+v, %v", rep2, err)
	}
}

func TestImportRunsRefusesTamperedEvidence(t *testing.T) {
	src := recordedCopy(t)
	// Change one byte of one sealed run file in the copy.
	var target string
	_ = filepath.WalkDir(filepath.Join(src, "e_smoke_sdoj"), func(p string, d os.DirEntry, err error) error {
		if err == nil && target == "" && !d.IsDir() && d.Name() == "run.json" {
			target = p
		}
		return nil
	})
	if target == "" {
		t.Fatal("no run.json in the recorded copy")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, ' ')
	if err := os.WriteFile(target, b, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "orgs", "org_demo")
	rep, err := ImportRuns(context.Background(), src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	var refused bool
	for _, e := range rep.Experiments {
		if e.ExpID == "e_smoke_sdoj" {
			refused = e.Status == "refused"
		}
	}
	if !refused {
		t.Fatalf("tampered experiment imported: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(dst, "e_smoke_sdoj")); !os.IsNotExist(err) {
		t.Fatal("refused experiment left in the org")
	}
}

func TestImportRefusesOverlap(t *testing.T) {
	src := t.TempDir()
	if _, err := ImportRuns(context.Background(), src, filepath.Join(src, "orgs", "x"), nil); err == nil {
		t.Fatal("destination inside source accepted")
	}
}
