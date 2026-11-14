//go:build integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestIntegrationTaskFromRepo is the "tasks from your own repository" path
// end to end with only the binary's code and local images: init a project,
// create a suite, cut a task from a temporary git repository, edit its
// instruction, validate it and admit it with the real environment and
// grader. Admission must pass every check and record the determinism
// repeats in suite.lock. Python runs the hidden_tests grader on
// bench/coding-core-base:1; Node runs verifier_command on node:22-slim.
// Nothing is pulled (the host's DNS is not trusted in tests) and nothing
// calls a model. Admission removes every container and volume it creates.
func TestIntegrationTaskFromRepo(t *testing.T) {
	if out, err := exec.Command("docker", "image", "inspect", "bench/coding-core-base:1").CombinedOutput(); err != nil {
		t.Skipf("bench/coding-core-base:1 is not available locally: %s", out)
	}
	tests := []struct {
		name, template, image string
		source                func(t *testing.T) (string, string, string)
		instruction           string
	}{
		{"python", "python-pytest", "bench/coding-core-base:1", pythonSource,
			"# Keep signs in add\n\n`calc.core.add(-2, 3)` returns 5; it must return the plain sum, 1.\n"},
		{"node", "node-test", "node:22-slim", nodeSource,
			"# Clamp the upper bound\n\n`clamp(50, 0, 10)` in src/math.js returns 50; it must stay between lo and hi.\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if out, err := exec.Command("docker", "image", "inspect", tc.image).CombinedOutput(); err != nil {
				t.Skipf("%s is not available locally: %s", tc.image, out)
			}
			src, base, ref := tc.source(t)
			t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
			dir := t.TempDir()
			config := filepath.Join(dir, "deploy", "bench.yaml")
			steps := [][]string{
				{"suite", "init", "s", "-template", tc.template, "-image", tc.image, "-config", config},
				{"task", "init", "-config", config, "-suite", "s", "-id", "t01", "-repo", src, "-base", base, "-reference", ref},
			}
			if code := runInit([]string{dir}); code != exitOK {
				t.Fatalf("init exit %d", code)
			}
			for _, s := range steps {
				if code := commands[s[0]].run(s[1:]); code != exitOK {
					t.Fatalf("%v exit %d", s, code)
				}
			}
			task := filepath.Join(dir, "benchmarks", "s", "tasks", "t01")
			if err := os.WriteFile(filepath.Join(task, "instruction.md"), []byte(tc.instruction), 0o644); err != nil {
				t.Fatal(err)
			}
			if code := runTask([]string{"validate", "-config", config, "s/t01"}); code != exitOK {
				t.Fatalf("validate exit %d", code)
			}
			if code := runTask([]string{"admit", "-config", config, "s/t01"}); code != exitOK {
				t.Fatalf("admit exit %d", code)
			}
			assertAdmitted(t, filepath.Join(dir, "benchmarks", "s", "suite.lock"))
		})
	}
}

// assertAdmitted reads suite.lock and checks the admission record of t01.
func assertAdmitted(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock schemas.SuiteLock
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	tl := lock.Tasks["t01"]
	rec := tl.Admission
	if tl.Status != schemas.TaskAdmitted || rec == nil || !rec.Admitted {
		t.Fatalf("t01 not admitted: %+v", tl)
	}
	if rec.ReferenceRepeats != 3 || strings.Join(rec.ReferenceResults, ",") != "pass,pass,pass" {
		t.Errorf("repeats %d results %v", rec.ReferenceRepeats, rec.ReferenceResults)
	}
	seen := map[string]bool{}
	for _, c := range rec.Checks {
		seen[c.Name] = true
	}
	for _, want := range []string{"reference_1", "reference_2", "reference_3", "empty_patch", "exploit", "null_noop", "null_refuse",
		"instruction_edited", "secret_scan", "verifier_no_network"} {
		if !seen[want] {
			t.Errorf("admission lacks check %s", want)
		}
	}
}

// nodeSource builds a Node repository whose fix commit changes a test and
// adds one beside the code.
func nodeSource(t *testing.T) (dir, base, ref string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir = filepath.Join(t.TempDir(), "lib")
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { clamp } from '../src/math.js';\n\n"
	write("package.json", `{"name":"mathlib","type":"module"}`+"\n")
	write("src/math.js", "export function clamp(x, lo, hi) {\n  return Math.max(lo, x);\n}\n")
	write("test/math.test.js", head+"test('clamps below', () => { assert.equal(clamp(-5, 0, 10), 0); });\n")
	gitFixture(t, dir, "init", "-q", "-b", "main")
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Add clamp")
	base = gitFixture(t, dir, "rev-parse", "HEAD")
	write("src/math.js", "export function clamp(x, lo, hi) {\n  return Math.min(hi, Math.max(lo, x));\n}\n")
	write("test/math.test.js", head+"test('clamps below', () => { assert.equal(clamp(-5, 0, 10), 0); });\ntest('clamps above', () => { assert.equal(clamp(50, 0, 10), 10); });\n")
	write("src/inside.test.js", "import { test } from 'node:test';\nimport assert from 'node:assert/strict';\nimport { clamp } from './math.js';\n\ntest('inside', () => { assert.equal(clamp(4, 0, 10), 4); });\n")
	gitFixture(t, dir, "add", "-A")
	gitFixture(t, dir, "commit", "-q", "-m", "Clamp the upper bound too")
	ref = gitFixture(t, dir, "rev-parse", "HEAD")
	return dir, base, ref
}
