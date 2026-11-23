package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/jobs"
)

type hw18Log struct {
	mu    sync.Mutex
	lines []string
}

func (l *hw18Log) add(f string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(f, a...))
}
func (l *hw18Log) Printf(f string, a ...any)   { l.add(f, a...) }
func (l *hw18Log) Warnf(f string, a ...any)    { l.add(f, a...) }
func (l *hw18Log) Errorf(f string, a ...any)   { l.add(f, a...) }
func (l *hw18Log) Write(p []byte) (int, error) { l.add("%s", p); return len(p), nil }

// A catalog import that writes no task logs every skipped task and keeps
// the result with the skip list on the failed job (fix wave 3, N263).
func TestCatalogImportJobLogsSkipsOnFailure(t *testing.T) {
	var spec *jobKindSpec
	for i := range jobKinds {
		if jobKinds[i].Kind == handlers.CatalogImportKind && jobKinds[i].Hosted {
			spec = &jobKinds[i]
		}
	}
	if spec == nil {
		t.Fatal("catalog.import is not registered")
	}
	root := t.TempDir()
	from := filepath.Join(root, "uploads", "datasets", "import-harbor", "zz-task-1")
	for p, body := range map[string]string{
		"task.toml":      "schema_version = \"1.1\"\n\n[task]\nname = \"zz/zz-task-1\"\n\n[verifier]\ntimeout_sec = 60.0\n\n[agent]\ntimeout_sec = 600.0\n",
		"instruction.md": "Write hello.\n",
		"tests/test.sh":  "#!/bin/sh\nexit 0\n",
	} {
		full := filepath.Join(from, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	params, _ := json.Marshal(map[string]any{"entry": "import-harbor", "from": "uploads/datasets/import-harbor/zz-task-1", "suite": "zz-test-harbor", "limit": 5})
	log := &hw18Log{}
	out, err := spec.Run(context.Background(), jobEnv{ProjectRoot: root, Hosted: true}, &jobs.Job{ID: "job_hw18", Params: params}, log, func(any) {})
	if err == nil {
		t.Fatal("the import of a task without a Dockerfile succeeded")
	}
	text := strings.Join(log.lines, "\n")
	if !strings.Contains(text, "skipped zz-task-1: no environment/Dockerfile") {
		t.Fatalf("job log does not list the skipped task:\n%s", text)
	}
	res, ok := out.(*importers.Result)
	if !ok || res == nil || len(res.Skipped) != 1 {
		t.Fatalf("failed job result %#v, want the import result with one skipped task", out)
	}
}
