package benchcatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (Source, string) {
	root := t.TempDir()
	write(t, filepath.Join(root, "agents", "codex.yaml"), "id: codex\nbuild:\n  dockerfile: agents/images/codex/Dockerfile\n")
	write(t, filepath.Join(root, "agents", "images", "codex", "Dockerfile"), "FROM scratch\n")
	write(t, filepath.Join(root, "agents", "noop.yaml"), "id: noop\n")
	write(t, filepath.Join(root, "benchmarks", "coding-core", "suite.yaml"), "id: coding-core\ndescription: starter\n")
	write(t, filepath.Join(root, "benchmarks", "hd-harbor-tb2", "suite.yaml"), "id: hd-harbor-tb2\nimported:\n  importer: harbor\n")
	write(t, filepath.Join(root, "benchmarks", "extra-suite", "suite.yaml"), "id: extra-suite\ndescription: extra\n")
	ex := filepath.Join(root, "examples", "acme-demo")
	write(t, filepath.Join(ex, "README.md"), "# Acme demo example\n\nA small demo project.\nSecond line.\n\nMore.\n")
	write(t, filepath.Join(ex, "benchmarks", "acme-demo", "suite.yaml"), "id: acme-demo\n")
	write(t, filepath.Join(ex, "agents", "demo.yaml"), "id: demo\nbuild:\n  dockerfile: agents/images/codex/Dockerfile\n")
	write(t, filepath.Join(ex, "deploy", "bench.yaml"), "x: 1\n")
	return Source{Root: root, Examples: true}, t.TempDir()
}

// TestExamplesOffByDefault: the Acme example projects are developer content;
// a Source without Examples (every hosted org) never lists or adds them.
func TestExamplesOffByDefault(t *testing.T) {
	src, project := fixture(t)
	src.Examples = false
	for _, e := range src.List(project) {
		if e.Kind == KindExample {
			t.Fatalf("example listed without Examples: %+v", e)
		}
	}
	if _, ok := src.Find(project, "example-acme-demo"); ok {
		t.Fatal("example found without Examples")
	}
	if _, ok := src.Find(project, "suite-extra-suite"); !ok {
		t.Fatal("bundled suites must still be listed")
	}
}

func TestDevContent(t *testing.T) {
	t.Setenv(DevEnv, "1")
	if !DevContent(false) {
		t.Fatal("dev flag ignored locally")
	}
	if DevContent(true) {
		t.Fatal("a hosted server offered developer content")
	}
	t.Setenv(DevEnv, "")
	if DevContent(false) {
		t.Fatal("developer content on without the flag")
	}
}

// TestStarterHasNoControls: a new org never gets the scripted controls.
func TestStarterHasNoControls(t *testing.T) {
	for _, a := range StarterAgents {
		if a == "oracle" || a == "noop" {
			t.Fatalf("starter agents include %s", a)
		}
	}
}

func TestListAndAdd(t *testing.T) {
	src, project := fixture(t)
	byID := map[string]Entry{}
	for _, e := range src.List(project) {
		byID[e.ID] = e
	}
	tb2, ok := byID["terminal-bench-2"]
	if !ok || tb2.Kind != KindBundled || !tb2.Available {
		t.Fatalf("terminal-bench-2 = %+v, want bundled and available", tb2)
	}
	if e := byID["livecodebench"]; e.Kind != KindImporter || !e.NeedsData {
		t.Fatalf("livecodebench = %+v, want importer needing data", e)
	}
	ex, ok := byID["example-acme-demo"]
	if !ok || ex.Title != "Acme demo example" || ex.Summary != "A small demo project. Second line." || len(ex.Suites) != 1 {
		t.Fatalf("example = %+v", ex)
	}
	if _, ok := byID["suite-extra-suite"]; !ok {
		t.Fatal("bundled extra-suite missing")
	}
	if _, ok := byID["suite-hd-harbor-tb2"]; ok {
		t.Fatal("hd-harbor-tb2 listed twice")
	}

	res, err := src.Add(project, ex)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"benchmarks/acme-demo/suite.yaml", "agents/demo.yaml", "agents/images/codex/Dockerfile"} {
		if _, err := os.Stat(filepath.Join(project, p)); err != nil {
			t.Fatalf("%s not copied: %v (result %+v)", p, err, res)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "deploy")); err == nil {
		t.Fatal("deploy folder of the example must not be copied")
	}
	again, err := src.Add(project, ex)
	if err != nil || len(again.Written) != 0 || len(again.Skipped) == 0 {
		t.Fatalf("second add = %+v, %v; want everything skipped", again, err)
	}
	e, _ := src.Find(project, "example-acme-demo")
	if !e.Installed {
		t.Fatal("example not reported installed after add")
	}
	if _, err := src.Add(project, Entry{ID: "x", Kind: KindImporter}); err != ErrNotCopyable {
		t.Fatalf("importer add err = %v", err)
	}
	if _, err := src.Add(project, Entry{ID: "suite-x", Kind: KindBundled, SuiteID: "../etc"}); err == nil {
		t.Fatal("escaping suite id accepted")
	}
}

func TestSeedOnce(t *testing.T) {
	src, project := fixture(t)
	res, err := src.Seed(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agents) != 1 || len(res.Suites) != 1 {
		t.Fatalf("seed = %+v", res)
	}
	for _, p := range []string{"agents/codex.yaml", "agents/images/codex/Dockerfile", "benchmarks/coding-core/suite.yaml", SeedMarker} {
		if _, err := os.Stat(filepath.Join(project, p)); err != nil {
			t.Fatalf("%s missing: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "agents", "noop.yaml")); err == nil {
		t.Fatal("the noop control was seeded into a new org")
	}
	// The org deletes an agent: a later start does not bring it back.
	_ = os.Remove(filepath.Join(project, "agents", "codex.yaml"))
	if _, err := src.Seed(project); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project, "agents", "codex.yaml")); err == nil {
		t.Fatal("seed ran twice")
	}
	// Local serve from the bench folder itself seeds nothing.
	if r, err := src.Seed(src.Root); err != nil || len(r.Written) != 0 {
		t.Fatalf("self seed = %+v, %v", r, err)
	}
}

func TestResolveFrom(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(project, "uploads", "data", "a.json"), "{}")
	if _, err := ResolveFrom(project, "uploads/data", true); err != nil {
		t.Fatalf("in-project path refused: %v", err)
	}
	if _, err := ResolveFrom(project, outside, true); err == nil {
		t.Fatal("hosted outside path accepted")
	}
	if _, err := ResolveFrom(project, "../"+filepath.Base(outside), true); err == nil {
		t.Fatal("hosted relative escape accepted")
	}
	if _, err := ResolveFrom(project, outside, false); err != nil {
		t.Fatalf("local outside path refused: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(project, "link")); err == nil {
		if _, err := ResolveFrom(project, "link", true); err == nil {
			t.Fatal("hosted symlink escape accepted")
		}
	}
	t.Setenv("HEIMDALL_DATASETS_DIR", outside)
	if _, err := ResolveFrom(project, outside, true); err != nil {
		t.Fatalf("datasets dir refused: %v", err)
	}
	if _, err := ResolveFrom(project, "", false); err == nil {
		t.Fatal("empty from accepted")
	}
}
