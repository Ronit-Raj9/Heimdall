package harbor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
)

// A one-task upload without environment/Dockerfile fails with the task and
// the reason in the error itself, and keeps the skip list (fix wave 3, N263).
func TestHarborNoTaskErrorNamesReason(t *testing.T) {
	from := filepath.Join(t.TempDir(), "zz-task-1")
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
	res, err := (&importer{now: time.Now}).Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: from, SuiteID: "zz-test-harbor"})
	if err == nil {
		t.Fatal("import of a task without a Dockerfile succeeded")
	}
	want := "zz-task-1: no environment/Dockerfile: a Harbor task needs the Dockerfile its container is built from"
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "see the skipped list") {
		t.Fatalf("error %q does not name the skipped task and its reason %q", err, want)
	}
	if res == nil || len(res.Skipped) != 1 {
		t.Fatalf("result %+v, want one skipped task", res)
	}
}

func TestNoTaskErrorListsThreeAndCounts(t *testing.T) {
	err := importers.NoTaskError("promptfoo", []importers.Skip{{ID: "a", Reason: "r1"}, {ID: "b", Reason: "r2"}, {ID: "c", Reason: "r3"}, {ID: "d", Reason: "r4"}, {ID: "e", Reason: "r5"}})
	want := "promptfoo: no task was imported. a: r1; b: r2; c: r3; and 2 more (listed in the job log)"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err, want)
	}
	if got := importers.NoTaskError("harbor", nil).Error(); got != "harbor: no task was imported" {
		t.Fatalf("no skips: %q", got)
	}
}
