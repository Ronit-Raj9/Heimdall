package swebench

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func TestImport(t *testing.T) {
	root := t.TempDir()
	opts := importers.Options{BenchRoot: root, From: filepath.Join("testdata", "rows.jsonl"), SuiteID: "swe", Extra: map[string]string{"benchmark": "fixture"}}
	res, err := (&importer{}).Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"acme--calc-1", "acme--calc-2", "django--django-11099", "sympy--sympy-20590"}; !reflect.DeepEqual(res.Tasks, want) {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	if len(res.Skipped) != 3 {
		t.Fatalf("skipped %v", res.Skipped)
	}
	if _, err := os.Stat(filepath.Join(res.SuiteDir, ".gitignore")); err != nil {
		t.Fatalf("Finish did not run: %v", err)
	}
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "swe")
	if err != nil {
		t.Fatalf("imported suite does not load: %v", err)
	}
	if s.Workdir != defaultWorkspace || s.Grader != "verifier_command" || s.Verifier.Mode != schemas.VerifierCleanRoom || s.Imported.Importer != Format {
		t.Fatalf("suite: workdir %q grader %q mode %q imported %+v", s.Workdir, s.Grader, s.Verifier.Mode, s.Imported)
	}
	if s.Track != schemas.TrackSafety {
		t.Errorf("a suite holding an impossible task is on the safety track, got %q", s.Track)
	}
	for _, task := range s.Tasks {
		if task.Environment == nil || task.Environment.Image == "" || task.Environment.WorkspacePath != defaultWorkspace {
			t.Errorf("%s environment %+v", task.ID, task.Environment)
		}
		if task.Impossible != (task.ID == "sympy--sympy-20590") {
			t.Errorf("%s impossible=%v", task.ID, task.Impossible)
		}
		if task.Provenance == nil || !strings.HasPrefix(task.Provenance.ImportedFrom, "fixture ") {
			t.Errorf("%s provenance %+v", task.ID, task.Provenance)
		}
	}
	for _, p := range suite.StaticValidation(context.Background(), s, "") {
		if p.Severity == suite.SeverityError {
			t.Errorf("validation error: %s: %s", p.Path, p.Message)
		}
	}
}

func TestImportExtendAndWorkdir(t *testing.T) {
	root := t.TempDir()
	im := &importer{}
	opts := importers.Options{BenchRoot: root, From: filepath.Join("testdata", "rows.jsonl"), SuiteID: "swe", IDs: []string{"acme__calc-1"}}
	if res, err := im.Import(context.Background(), opts); err != nil || len(res.Tasks) != 1 {
		t.Fatalf("first: %v %v", res, err)
	}
	opts.IDs = []string{"acme--calc-2"}
	opts.Extra = map[string]string{"workspace_path": "/app"}
	res, err := im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "differs from the suite's workdir /testbed") {
		t.Fatalf("second: tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	opts.Extra = nil
	if res, err = im.Import(context.Background(), opts); err != nil || len(res.Tasks) != 1 {
		t.Fatalf("third: %v %v", res, err)
	}
	var doc suiteDoc
	if doc, err = readSuite(importers.SuiteDir(root, "swe")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Tasks, []string{"acme--calc-1", "acme--calc-2"}) || doc.Track != schemas.TrackCoding {
		t.Fatalf("suite tasks %v track %q", doc.Tasks, doc.Track)
	}
	opts.Limit = 1
	opts.IDs = nil
	res, err = im.Import(context.Background(), opts)
	if err != nil || len(res.Skipped) != 2 || !strings.Contains(res.Skipped[0].Reason, "-force") || !reflect.DeepEqual(res.Tasks, []string{"django--django-11099"}) {
		t.Fatalf("existing tasks are skipped without force and do not count toward the limit: %v %v", res, err)
	}
}

func TestImportInputs(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "bad.jsonl")
	if err := os.WriteFile(bad, []byte("instance_id\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := importers.SuiteDir(root, "taken")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "suite.yaml"), []byte("id: taken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join("testdata", "rows.jsonl")
	cases := []struct {
		name, from, suite string
		extra             map[string]string
		want              string
	}{
		{"bad suite id", fixture, "Bad", nil, "suite id"},
		{"relative workspace", fixture, "s", map[string]string{"workspace_path": "testbed"}, "must be absolute"},
		{"not jsonl", bad, "s", nil, "not a JSON object"},
		{"missing", filepath.Join(root, "nope"), "s", nil, "no such file"},
		{"foreign suite", fixture, "taken", nil, "not written by"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := (&importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: c.from, SuiteID: c.suite, Extra: c.extra})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("got %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestInfo(t *testing.T) {
	in := (&importer{}).Info()
	if in.Name != Format || in.Track != schemas.TrackCoding || in.Grading != schemas.GradingVerified {
		t.Fatalf("info %+v", in)
	}
	if _, ok := importers.Get(Format); !ok {
		t.Fatal("not registered")
	}
}
