package imagepub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformImagesFromCheckoutAndLibrary(t *testing.T) {
	checkout, library, org := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(checkout, "agents", "codex.yaml"), "id: codex\nbuild:\n  dockerfile: agents/images/codex/Dockerfile\n  tag: bench/agent-codex:1\n")
	writeFile(t, filepath.Join(checkout, "agents", "remote.yaml"), "id: remote\nimage: ghcr.io/acme/agent:1\n")
	s := filepath.Join(library, "benchmarks", "mini")
	if err := os.CopyFS(s, os.DirFS(filepath.Join("..", "suite", "testdata", "valid", "mini"))); err != nil {
		t.Fatal(err)
	}
	task := filepath.Join(s, "tasks", "m01", "task.yaml")
	raw, err := os.ReadFile(task)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, task, string(raw)+"environment:\n  dockerfile: Dockerfile\n")
	writeFile(t, filepath.Join(s, "tasks", "m01", "Dockerfile"), "FROM bench/coding-core-base:1\n")
	writeFile(t, filepath.Join(library, "benchmarks", "broken", "suite.yaml"), "id: [\n")
	// An organization's folder, passed nowhere: nothing of it may appear.
	writeFile(t, filepath.Join(org, "agents", "mine.yaml"), "id: mine\nbuild:\n  tag: bench/agent-mine:1\n")
	writeFile(t, filepath.Join(org, "benchmarks", "orgsuite", "suite.yaml"), "id: orgsuite\nbase_image:\n  tag: bench/orgsuite-base:1\n")

	set, err := PlatformImages(checkout, library)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bench/coding-core-base:1", "bench/agent-codex:1", "bench/mini-base:1"} {
		if _, ok := set[want]; !ok {
			t.Errorf("set lacks %s: %v", want, set)
		}
	}
	found := false
	for ref := range set {
		if strings.HasPrefix(ref, "bench/task-mini-m01:") {
			found = true
		}
		if strings.Contains(ref, "mine") || strings.Contains(ref, "orgsuite") || strings.Contains(ref, "ghcr.io") {
			t.Errorf("set holds %s", ref)
		}
	}
	if !found {
		t.Errorf("set lacks the task image of mini/m01: %v", set)
	}
	if set["bench/agent-codex:1"] != "agent codex" {
		t.Errorf("note = %q", set["bench/agent-codex:1"])
	}
}

func TestPlatformImagesEmptyRoots(t *testing.T) {
	set, err := PlatformImages("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 {
		t.Fatalf("set = %v, want only the core base", set)
	}
}
