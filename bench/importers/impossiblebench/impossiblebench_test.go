package impossiblebench

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func importOpts(t *testing.T, root string, extra map[string]string) importers.Options {
	t.Helper()
	return importers.Options{BenchRoot: root, From: filepath.Join("testdata", "lcb.jsonl"), SuiteID: "ib-lcb", Extra: extra}
}

func TestImportLCB(t *testing.T) {
	root := t.TempDir()
	im := &importer{}
	res, err := im.Import(context.Background(), importOpts(t, root, map[string]string{"solutions": filepath.Join("testdata", "solutions.jsonl")}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"toy-0-oneoff", "toy-0-conflicting", "toy-0-original"}; !reflect.DeepEqual(res.Tasks, want) {
		t.Fatalf("tasks %v, want %v", res.Tasks, want)
	}
	var reasons []string
	for _, s := range res.Skipped {
		reasons = append(reasons, s.ID+": "+s.Reason)
	}
	if len(res.Skipped) != 2 || !strings.Contains(reasons[0], "test equals original_test") || !strings.Contains(reasons[1], "not a Python identifier") {
		t.Fatalf("skipped %v", reasons)
	}
	if _, err := os.Stat(filepath.Join(res.SuiteDir, ".gitignore")); err != nil {
		t.Fatalf("Finish did not run: %v", err)
	}

	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "ib-lcb")
	if err != nil {
		t.Fatalf("imported suite does not load: %v", err)
	}
	if s.Track != schemas.TrackSafety || s.Imported == nil || s.Imported.Importer != Format || s.Imported.License != license {
		t.Fatalf("suite fields: track %q imported %+v", s.Track, s.Imported)
	}
	if !strings.Contains(s.Imported.Benchmark, "conflicting, oneoff, original") {
		t.Errorf("benchmark label %q", s.Imported.Benchmark)
	}
	for _, task := range s.Tasks {
		if task.Impossible != (task.ID != "toy-0-original") {
			t.Errorf("%s impossible=%v", task.ID, task.Impossible)
		}
		if task.Provenance == nil || task.Provenance.License != license || !strings.Contains(task.Provenance.ImportedFrom, "toy_0") {
			t.Errorf("%s provenance %+v", task.ID, task.Provenance)
		}
	}
	for _, p := range suite.StaticValidation(context.Background(), s, "") {
		if p.Severity == suite.SeverityError {
			t.Errorf("validation error: %s: %s", p.Path, p.Message)
		}
	}
}

func TestImportLimitIDsForce(t *testing.T) {
	root := t.TempDir()
	im := &importer{}
	opts := importOpts(t, root, nil)
	opts.Limit = 1
	res, err := im.Import(context.Background(), opts)
	if err != nil || !reflect.DeepEqual(res.Tasks, []string{"toy-0-oneoff"}) {
		t.Fatalf("limit: %v %v", res, err)
	}
	opts.Limit, opts.IDs = 0, []string{"toy-0-oneoff", "toy-0-conflicting"}
	res, err = im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Tasks, []string{"toy-0-conflicting"}) || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "-force") {
		t.Fatalf("second import: tasks %v skipped %v", res.Tasks, res.Skipped)
	}
	opts.Force = true
	if res, err = im.Import(context.Background(), opts); err != nil || len(res.Tasks) != 2 {
		t.Fatalf("force: %v %v", res, err)
	}
	b, err := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc schemas.Suite
	if err := suite.DecodeYAML(b, "suite.yaml", suite.SchemaSuite, &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.TaskIDs, []string{"toy-0-oneoff", "toy-0-conflicting"}) {
		t.Fatalf("suite tasks %v, want the first import's kept", doc.TaskIDs)
	}
}

func TestImportRefusesForeignSuite(t *testing.T) {
	root := t.TempDir()
	dir := importers.SuiteDir(root, "ib-lcb")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte("id: ib-lcb\ntasks: [t01]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&importer{}).Import(context.Background(), importOpts(t, root, nil)); err == nil || !strings.Contains(err.Error(), "not written by") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestImportInputs(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name  string
		from  string
		suite string
		extra map[string]string
		want  string
	}{
		{"bad suite id", filepath.Join("testdata", "lcb.jsonl"), "Bad_Suite", nil, "suite id"},
		{"missing file", filepath.Join(root, "nope.jsonl"), "s", nil, "no such file"},
		{"not json", write("bad.jsonl", "task_id,prompt\n"), "s", nil, "not a JSON object"},
		{"empty", write("empty.jsonl", "\n\n"), "s", nil, "no rows"},
		{"unknown shape", write("other.jsonl", `{"question": "q"}`+"\n"), "s", nil, "neither entry_point"},
		{"bad solutions", filepath.Join("testdata", "lcb.jsonl"), "s", map[string]string{"solutions": write("sol.jsonl", `{"task_id": "x"}`+"\n")}, "needs task_id and solution"},
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

// fakeSWE stands in for the swebench importer, which this test binary does
// not link, and records the options it was handed.
type fakeSWE struct{ got importers.Options }

func (f *fakeSWE) Info() importers.Info { return importers.Info{Name: sweFormat} }

func (f *fakeSWE) Import(_ context.Context, opts importers.Options) (*importers.Result, error) {
	f.got = opts
	return &importers.Result{Suite: opts.SuiteID}, nil
}

var (
	fake     = &fakeSWE{}
	fakeOnce sync.Once
)

func TestImportSWEDelegates(t *testing.T) {
	root := t.TempDir()
	from := filepath.Join(root, "swe.jsonl")
	if err := os.WriteFile(from, []byte(`{"instance_id": "a__b-1", "impossible_type": "oneoff"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeOnce.Do(func() { importers.Register(fake) })
	_, err := (&importer{}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "s", Extra: map[string]string{"license": "MIT", "test_cmd": "pytest"}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"impossible": "true", "benchmark": "impossiblebench/swebench", "source": sweSource, "license": "MIT", "test_cmd": "pytest"}
	if !reflect.DeepEqual(fake.got.Extra, want) || fake.got.SuiteID != "s" {
		t.Fatalf("delegated options %+v, want extra %v", fake.got, want)
	}
}

func TestReadRowsFolder(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"b.jsonl": `{"n": 2}` + "\n", "a.jsonl": `{"n": 1}` + "\n\n", "c.txt": `{"n": 3}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := readRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, string(r))
	}
	if !slices.Equal(got, []string{`{"n": 1}`, `{"n": 2}`}) {
		t.Fatalf("rows %v", got)
	}
}

func TestInfo(t *testing.T) {
	in := (&importer{}).Info()
	if in.Name != Format || in.Track != schemas.TrackSafety || in.Grading != schemas.GradingVerified || in.License != license {
		t.Fatalf("info %+v", in)
	}
	if _, ok := importers.Get(Format); !ok {
		t.Fatal("not registered")
	}
}
