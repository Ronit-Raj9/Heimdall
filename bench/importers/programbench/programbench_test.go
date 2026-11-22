package programbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	_ "github.com/maximhq/bifrost/bench/importers/harbor"
)

func TestImportFixture(t *testing.T) {
	root := t.TempDir()
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("programbench importer not registered")
	}
	res, err := im.Import(context.Background(), importers.Options{
		BenchRoot: root,
		From:      "testdata",
		SuiteID:   "programbench",
		Extra:     map[string]string{SetAgentTimeout: "1800", SetRevision: "deadbeef"},
	})
	if err != nil {
		t.Fatalf("import: %v (skipped %v)", err, res)
	}
	if len(res.Tasks) != 1 || res.Tasks[0] != "acme__tool.1234567" {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	var noblob bool
	for _, s := range res.Skipped {
		if s.ID == "testorg__calculator.abc1234" {
			t.Errorf("upstream fixture task must not be listed: %v", s)
		}
		if s.ID == "acme__noblob.89abcde" && strings.Contains(s.Reason, "missing") {
			noblob = true
		}
	}
	if !noblob {
		t.Errorf("task without test blobs not skipped with a reason: %v", res.Skipped)
	}
	dir := filepath.Join(res.SuiteDir, "tasks", "acme__tool.1234567")
	var found struct{ branch, ignored, hashes bool }
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		switch filepath.Base(p) {
		case "b1.tar.gz":
			found.branch = true
		case "b2.tar.gz":
			found.ignored = true
		case "clean_hashes.txt":
			found.hashes = true
		}
		return nil
	})
	if !found.branch || found.ignored || !found.hashes {
		t.Errorf("verifier files: %+v", found)
	}
	b, err := os.ReadFile(filepath.Join(dir, "task.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "agent_s: 1800") {
		t.Errorf("task.yaml lacks the agent cap:\n%s", b)
	}
	df, err := os.ReadFile(filepath.Join(dir, "environment", "Dockerfile"))
	if err != nil || !strings.Contains(string(df), "FROM programbench/acme_1776_tool.1234567:task_cleanroom_v6") {
		t.Errorf("Dockerfile does not start from the upstream image: %s %v", df, err)
	}
}

func TestSettings(t *testing.T) {
	if _, err := readSettings("x", map[string]string{"bogus": "1"}); err == nil {
		t.Error("unknown setting accepted")
	}
	if _, err := readSettings("x", map[string]string{SetAgentTimeout: "-3"}); err == nil {
		t.Error("negative timeout accepted")
	}
}
