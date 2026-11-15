package traceexport

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLoadRunRecorded(t *testing.T) {
	in, err := LoadRun(filepath.Join(recordedRoot, recordedRuns[0]))
	if err != nil {
		t.Fatal(err)
	}
	if in.Run.RunID != "r_aunhv28sxj" || in.Slot.Task != "t01" || in.Slot.Arm != "B" {
		t.Fatalf("identity: %+v %+v", in.Run.RunID, in.Slot)
	}
	if len(in.SealHash) != 64 || in.Evidence.Requests == nil || len(in.Transitions) == 0 || len(in.Metrics) == 0 {
		t.Fatal("run files not loaded")
	}
}

func TestLoadFiles(t *testing.T) {
	files := map[string]string{
		"run.json":          `{"run_id":"r_a","timing":{"started":"2026-11-15T10:00:00Z"}}`,
		"seal.json":         `{"run_id":"r_a"}`,
		"transitions.jsonl": "{\"stage\":\"grade\",\"status\":\"started\",\"at\":\"2026-11-15T10:00:01Z\"}\n\n",
	}
	read := func(m map[string]string) ReadFunc {
		return func(name string) ([]byte, error) {
			s, ok := m[name]
			if !ok {
				return nil, fmt.Errorf("%s: %w", name, schemas.ErrNotFound)
			}
			return []byte(s), nil
		}
	}
	in, err := LoadFiles(read(files))
	if err != nil {
		t.Fatal(err)
	}
	if in.Run.RunID != "r_a" || len(in.Transitions) != 1 || in.SealHash == "" {
		t.Fatalf("loaded %+v", in)
	}
	unsealed := map[string]string{"run.json": files["run.json"]}
	if _, err := LoadFiles(read(unsealed)); !errors.Is(err, ErrUnsealed) {
		t.Fatalf("unsealed run: got %v", err)
	}
	if _, err := LoadFiles(read(map[string]string{})); err == nil {
		t.Fatal("a folder without run.json loaded")
	}
	broken := map[string]string{"run.json": files["run.json"], "seal.json": "{}", "evidence.json": "{"}
	if _, err := LoadFiles(read(broken)); err == nil {
		t.Fatal("a broken evidence.json loaded")
	}
	failing := func(string) ([]byte, error) { return nil, fs.ErrPermission }
	if _, err := LoadFiles(failing); err == nil {
		t.Fatal("a read error was ignored")
	}
}

func TestMerge(t *testing.T) {
	a := &TracesData{ResourceSpans: []ResourceSpans{{}}}
	b := &TracesData{ResourceSpans: []ResourceSpans{{}, {}}}
	if got := len(Merge(a, nil, b).ResourceSpans); got != 3 {
		t.Fatalf("merged %d resource spans, want 3", got)
	}
	if Merge().ResourceSpans == nil {
		t.Fatal("an empty merge must still marshal resourceSpans as []")
	}
}
