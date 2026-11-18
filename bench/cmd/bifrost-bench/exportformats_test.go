package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/export"
)

// W10 export formats, run on the recorded $0 smoke experiment.

func runFormat(t *testing.T, format string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runExportFormat(format, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestExportEEERecordedSmoke(t *testing.T) {
	code, _, stderr := runFormat(t, "eee", "-runs", recordedRunsRoot, "-exp", smokeExp)
	if code != exitUsage || !strings.Contains(stderr, "synthetic") {
		t.Fatalf("a smoke experiment must be refused without -allow-synthetic: exit %d %s", code, stderr)
	}
	code, stdout, stderr := runFormat(t, "eee", "-runs", recordedRunsRoot, "-exp", smokeExp, "-allow-synthetic", "-org", "Test Org")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		t.Fatal("no record")
	}
	for _, l := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatal(err)
		}
		if err := export.ValidateEEE(rec); err != nil {
			t.Fatalf("record does not validate: %v", err)
		}
		d := rec["source_metadata"].(map[string]any)["additional_details"].(map[string]any)
		if d["heimdall_synthetic"] != "true" || d["heimdall_manifest_hash"] == "" || d["heimdall_seal_set_sha256"] == "" {
			t.Fatalf("provenance missing: %v", d)
		}
	}
	dir := filepath.Join(t.TempDir(), "eee") + "/"
	if code, _, stderr := runFormat(t, "eee", "-runs", recordedRunsRoot, "-exp", smokeExp, "-allow-synthetic", "-out", dir); code != exitOK {
		t.Fatalf("folder out: exit %d %s", code, stderr)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != len(lines) {
		t.Fatalf("%d files for %d records", len(files), len(lines))
	}
}

func TestExportRunTableRecorded(t *testing.T) {
	code, stdout, stderr := runFormat(t, "jsonl", "-runs", recordedRunsRoot, "-exp", smokeExp)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var row export.RunRow
		if err := json.Unmarshal([]byte(l), &row); err != nil {
			t.Fatal(err)
		}
		if row.RunID == "" || row.SealHash == "" || !row.Synthetic {
			t.Fatalf("row %+v", row)
		}
		n++
	}
	out := filepath.Join(t.TempDir(), "runs.csv")
	if code, _, stderr := runFormat(t, "csv", "-runs", recordedRunsRoot, "-exp", smokeExp, "-out", out); code != exitOK {
		t.Fatalf("csv: exit %d %s", code, stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(b)), "\n")); got != n+1 {
		t.Fatalf("csv has %d lines, want header + %d", got, n)
	}
}

func TestExportOTLPPresetToFakeCollector(t *testing.T) {
	var mu sync.Mutex
	var paths, ctypes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		paths, ctypes = append(paths, r.URL.Path), append(ctypes, r.Header.Get("Content-Type"))
		mu.Unlock()
	}))
	defer srv.Close()
	var stdout, stderr bytes.Buffer
	code := runExportOTLP([]string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-to", "phoenix", "-endpoint", srv.URL}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) == 0 || paths[0] != "/v1/traces" || ctypes[0] != "application/x-protobuf" {
		t.Fatalf("paths %v types %v", paths, ctypes)
	}
	stderr.Reset()
	if code := runExportOTLP([]string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-to", "datadog"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("datadog without -endpoint: exit %d", code)
	}
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	if code := runExportOTLP([]string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-to", "langfuse", "-endpoint", srv.URL}, &stdout, &stderr); code != exitInfra {
		t.Fatalf("langfuse without keys: exit %d", code)
	}
	if code := runExportOTLP([]string{"-runs", recordedRunsRoot, "-exp", smokeExp, "-to", "nope"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("unknown preset: exit %d", code)
	}
}

func TestExportHarborCmd(t *testing.T) {
	root := t.TempDir()
	abs, err := filepath.Abs("../../benchmarks")
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "bench.yaml")
	if err := os.WriteFile(cfg, []byte("runs_root: runs\nbenchmarks_root: "+abs+"\nserver:\n  listen: 127.0.0.1:1\n  mode: live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "harbor")
	if code, _, stderr := runFormat(t, "harbor", "-config", cfg, "-suite", "coding-core", "-out", out); code != exitUsage || !strings.Contains(stderr, "-include-hidden") {
		t.Fatalf("without -include-hidden: exit %d %s", code, stderr)
	}
	code, _, stderr := runFormat(t, "harbor", "-config", cfg, "-suite", "coding-core", "-tasks", "t01,t02", "-out", out, "-include-hidden")
	if code != exitOK || !strings.Contains(stderr, "warning:") {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, p := range []string{"t01/task.toml", "t02/tests/test.sh", "t01/solution/solve.sh"} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("missing %s", p)
		}
	}
	if code, _, _ := runFormat(t, "harbor", "-config", cfg, "-suite", "coding-core", "-tasks", "t01", "-out", out, "-include-hidden"); code != exitInfra {
		t.Fatalf("existing folder without -force: exit %d", code)
	}
	if code, _, _ := runFormat(t, "inspect"); code != exitUsage {
		t.Fatal("export inspect must say why it is not written")
	}
}

func TestPresetEndpoint(t *testing.T) {
	cases := map[[2]string]string{
		{"https://us.cloud.langfuse.com", "https://cloud.langfuse.com/api/public/otel/v1/traces"}: "https://us.cloud.langfuse.com/api/public/otel/v1/traces",
		{"https://eu.example.com/custom/v1/traces", "https://x/api/public/otel/v1/traces"}:        "https://eu.example.com/custom/v1/traces",
		{"http://127.0.0.1:4318", ""}:           "http://127.0.0.1:4318",
		{"", "https://api.getmaxim.ai/v1/otel"}: "",
	}
	for in, want := range cases {
		if got := presetEndpoint(in[0], in[1]); got != want {
			t.Errorf("presetEndpoint(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
