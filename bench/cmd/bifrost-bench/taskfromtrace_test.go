package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fromTraceSecret is an API-key-shaped value planted in the test trace,
// built at run time so no key-shaped string sits in the repository.
var fromTraceSecret = "sk-" + "TESTONLY" + strings.Repeat("q7", 10)

// captureOutput runs f with stdout redirected and returns what it printed.
func captureOutput(t *testing.T, f func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	code := f()
	os.Stdout = old
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return code, string(<-done)
}

func TestRunTaskFromTrace(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "support", "-template", "qa", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	trace := filepath.Join(dir, "sessions.jsonl")
	body := `{"id":"conv-1","status":"failed","messages":[{"role":"user","content":"What is the hotel cap in Zurich? I am jane.doe@example.com, key ` + fromTraceSecret + `"},{"role":"assistant","content":"CHF 180"}]}
{"id":"conv-2","messages":[{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello"}]}
`
	if err := os.WriteFile(trace, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := captureOutput(t, func() int { return runTask([]string{"from-trace", "-list", trace}) })
	if code != exitOK || !strings.Contains(out, "conv-1") || !strings.Contains(out, "conv-2") || !strings.Contains(out, "error") || strings.Contains(out, "jane.doe") {
		t.Fatalf("-list exit %d:\n%s", code, out)
	}
	base := []string{"from-trace", "-config", config, "-suite", "support", "-trace", trace}
	if code := runTask(append(base, "-id", "t1")); code == exitOK {
		t.Fatal("two sessions and no -session: exit 0")
	}
	code, out = captureOutput(t, func() int { return runTask(append(base, "-id", "zurich-cap", "-session", "conv-1")) })
	if code != exitOK {
		t.Fatalf("from-trace exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "redacted: 1 api key, 1 email") || strings.Contains(out, fromTraceSecret) || strings.Contains(out, "possible") {
		t.Errorf("output:\n%s", out)
	}
	taskDir := filepath.Join(dir, "benchmarks", "support", "tasks", "zurich-cap")
	checkTreeLacks(t, taskDir, fromTraceSecret, "jane.doe@example.com")
	if code := runTask([]string{"validate", "-config", config, "support/zurich-cap"}); code != 1 {
		t.Fatalf("validate a draft with EDIT ME markers: exit %d, want 1", code)
	}
	dropEditMeLines(t, taskDir)
	if code := runTask([]string{"validate", "-config", config, "support/zurich-cap"}); code != exitOK {
		t.Fatalf("validate after the EDIT ME lines are gone: exit %d", code)
	}
	for _, bad := range [][]string{
		{"from-trace", "-config", config, "-suite", "support", "-id", "t2"},
		{"from-trace", "-config", config, "-suite", "support", "-id", "t3", "-trace", trace, "-session", "conv-2", "-max-bytes", "10"},
		{"from-trace", "-config", config, "-suite", "support", "-id", "t4", "-trace", filepath.Join(dir, "missing.json")},
		{"from-trace", "-config", config, "-suite", "support", "-id", "t5", "-trace", trace, "-session", "conv-2", "-format", "otlp"},
		{"from-trace", "-config", config, "-suite", "support", "-id", "t6", "-trace", trace, "-session", "conv-2", "-kind", "browser"},
		{"from-trace", "stray"},
	} {
		if code := runTask(bad); code == exitOK {
			t.Errorf("task %v: exit 0", bad)
		}
	}
	for _, id := range []string{"t3", "t4", "t5", "t6"} {
		if _, err := os.Stat(filepath.Join(dir, "benchmarks", "support", "tasks", id)); err == nil {
			t.Errorf("a refused draft left tasks/%s behind", id)
		}
	}
}

// checkTreeLacks fails when any file under dir holds one of the values.
func checkTreeLacks(t *testing.T, dir string, values ...string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		for _, v := range values {
			if bytes.Contains(b, []byte(v)) {
				t.Errorf("%s holds %q", p, v)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// dropEditMeLines deletes every EDIT ME line under dir, as a person does
// after confirming each judgement.
func dropEditMeLines(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var kept []string
		for _, line := range strings.Split(string(b), "\n") {
			if l := strings.TrimSpace(line); !strings.HasPrefix(l, "<!-- EDIT ME -->") && !strings.HasPrefix(l, "# EDIT ME") {
				kept = append(kept, line)
			}
		}
		return os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
