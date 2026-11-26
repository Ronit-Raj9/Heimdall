package swebench

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
)

// w446Row is the first fixture row with Heimdall's language fields added.
func w446Row(t *testing.T, lang string, restore []string) string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "rows.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	if !sc.Scan() {
		t.Fatal("empty fixture")
	}
	var m map[string]any
	if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	m["heimdall_language"] = lang
	m["heimdall_restore"] = restore
	m["test_cmds"] = []string{"go test ./..."}
	b, _ := json.Marshal(m)
	p := filepath.Join(t.TempDir(), "rows.jsonl")
	if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSwebenchUsesRowRestore(t *testing.T) {
	root := t.TempDir()
	from := w446Row(t, "go", []string{"go.mod", "go.sum"})
	res, err := (&importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "swe"})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("import: %v %v", res, err)
	}
	doc, err := readSuite(importers.SuiteDir(root, "swe"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Verifier.Restore, []string{"go.mod", "go.sum"}) {
		t.Fatalf("restore %v", doc.Verifier.Restore)
	}
	b, err := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", res.Tasks[0], "controls", "exploit.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "conftest") {
		t.Fatalf("a Go task got the pytest exploit:\n%s", b)
	}
}

func TestSwebenchDefaultRestoreUnchanged(t *testing.T) {
	root := t.TempDir()
	from := w446Row(t, "", nil)
	res, err := (&importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "swe"})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("import: %v %v", res, err)
	}
	doc, _ := readSuite(importers.SuiteDir(root, "swe"))
	if !reflect.DeepEqual(doc.Verifier.Restore, []string{"conftest.py", "pytest.ini", "tox.ini", "sitecustomize.py", "usercustomize.py"}) {
		t.Fatalf("restore %v", doc.Verifier.Restore)
	}
	b, _ := os.ReadFile(filepath.Join(res.SuiteDir, "tasks", res.Tasks[0], "controls", "exploit.sh"))
	if !strings.Contains(string(b), "conftest") {
		t.Fatal("a Python task lost the pytest exploit")
	}
}
