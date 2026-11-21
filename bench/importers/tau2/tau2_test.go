package tau2

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

const fixtureRef = "0123456789abcdef0123456789abcdef01234567"

func fixtureDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("testdata", "upstream"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func runImport(t *testing.T, extra map[string]string, ids []string, limit int) (*importers.Result, string) {
	t.Helper()
	root := t.TempDir()
	if extra == nil {
		extra = map[string]string{}
	}
	if _, ok := extra[SetRef]; !ok {
		extra[SetRef] = fixtureRef
	}
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{
		BenchRoot: root, From: fixtureDir(t), SuiteID: "tau2-fixture", IDs: ids, Limit: limit, Extra: extra,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return res, root
}

func skipIDs(res *importers.Result) []string {
	var out []string
	for _, s := range res.Skipped {
		out = append(out, s.ID)
	}
	slices.Sort(out)
	return out
}

func TestRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("tau2 importer not registered")
	}
	info := im.Info()
	if info.Track != schemas.TrackToolUse || info.Grading != schemas.GradingVerified || info.License != "MIT" {
		t.Fatalf("info = %+v", info)
	}
}

func TestImportPublished(t *testing.T) {
	res, root := runImport(t, nil, nil, 0)
	wantTasks := []string{"airline-a1", "airline-a3", "airline-a5", "retail-1", "retail-3"}
	if !slices.Equal(res.Tasks, wantTasks) {
		t.Fatalf("tasks = %v, want %v", res.Tasks, wantTasks)
	}
	wantSkips := []string{"airline-a2", "airline-a4", "airline-a6", "retail-2"}
	if got := skipIDs(res); !slices.Equal(got, wantSkips) {
		t.Fatalf("skipped = %v (%+v), want %v", got, res.Skipped, wantSkips)
	}
	dir := importers.SuiteDir(root, "tau2-fixture")
	if b, err := os.ReadFile(filepath.Join(dir, ".gitignore")); err != nil || !strings.Contains(string(b), "*") {
		t.Fatalf(".gitignore: %q, %v", b, err)
	}

	var s schemas.Suite
	if err := suite.DecodeYAMLFile(filepath.Join(dir, "suite.yaml"), "suite.yaml", suite.SchemaSuite, &s); err != nil {
		t.Fatalf("suite.yaml does not match the suite schema: %v", err)
	}
	if s.Track != schemas.TrackToolUse || s.Grader != "verifier_command" || s.Verifier.Result != schemas.VerifierResultReward {
		t.Fatalf("suite = %+v", s)
	}
	if s.Output == nil || s.Output.Kind != schemas.OutputState {
		t.Fatalf("suite output = %+v", s.Output)
	}
	if s.Imported == nil || s.Imported.Importer != Name || s.Imported.Revision != fixtureRef || s.Imported.License != "MIT" ||
		s.Imported.Benchmark != "tau2-bench@9.9.9 airline:text-v1/airline_50,retail:text-v1/retail_50" {
		t.Fatalf("imported = %+v", s.Imported)
	}
	if !slices.Equal(s.TaskIDs, wantTasks) {
		t.Fatalf("suite tasks = %v", s.TaskIDs)
	}

	// The English replacement, not the registry task, is what retail-1 holds.
	task := readTaskJSON(t, filepath.Join(dir, "tasks", "retail-1", "verifier", "tests", "task.json"))
	if !strings.Contains(string(task["user_scenario"]), "reviewed English version") {
		t.Fatalf("retail-1 is not the English pack task: %s", task["user_scenario"])
	}
}

func TestImportTaskFolder(t *testing.T) {
	res, root := runImport(t, nil, []string{"airline-a1"}, 0)
	if !slices.Equal(res.Tasks, []string{"airline-a1"}) {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	dir := filepath.Join(importers.SuiteDir(root, "tau2-fixture"), "tasks", "airline-a1")
	var task schemas.Task
	if err := suite.DecodeYAMLFile(filepath.Join(dir, "task.yaml"), "task.yaml", suite.SchemaTask, &task); err != nil {
		t.Fatalf("task.yaml does not match the task schema: %v", err)
	}
	if task.Output == nil || task.Output.Kind != schemas.OutputState || len(task.Services) != 1 {
		t.Fatalf("task = %+v", task)
	}
	sv := task.Services[0]
	if sv.ID != "tau2" || sv.Role != schemas.ServiceRoleMCP || sv.Port != 8000 || sv.Ready == nil || sv.Ready.HTTP != "/healthz" {
		t.Fatalf("service = %+v", sv)
	}
	// The verifier runs in the service image under the tag the run gives
	// the service build; the agent runs in the clean workspace image.
	svcImg, err := utils.BuildImageSpec(dir, "service/Dockerfile", "", "svc-tau2-fixture-tau2", true)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Build == nil || sv.Build.Tag != svcImg.Tag || !strings.HasPrefix(svcImg.Tag, "bench/svc-tau2-fixture-tau2:") {
		t.Fatalf("service build %+v, want tag %s", sv.Build, svcImg.Tag)
	}
	env := task.Environment
	if env == nil || env.VerifierImage != svcImg.Tag || env.Dockerfile != "workspace/Dockerfile" || !env.BuildNetwork || env.Image != "" {
		t.Fatalf("environment = %+v", env)
	}
	if !strings.HasPrefix(res.Notes[0], "the first run builds the service image "+svcImg.Tag) {
		t.Fatalf("notes = %v", res.Notes)
	}
	if len(sv.Export) != 2 || sv.Export[0].ID != "db.json" || sv.Export[1].ID != "transcript.json" {
		t.Fatalf("exports = %+v", sv.Export)
	}
	if task.Provenance == nil || task.Provenance.ImportedFrom != "tau2-bench airline task a1" || task.Provenance.BaseCommit != fixtureRef {
		t.Fatalf("provenance = %+v", task.Provenance)
	}
	if !slices.Equal(task.Controls.Mutants, []string{"controls/mutant1.sh"}) {
		t.Fatalf("mutants = %v", task.Controls.Mutants)
	}
	for _, f := range []string{"instruction.md", "repo/README.md", "workspace/Dockerfile", "service/Dockerfile", "service/tau2svc.py", "scenario/scenario.json",
		"verifier/tests/task.json", "verifier/tests/grading.json", "controls/reference.sh", "controls/empty.sh", "controls/exploit.sh", "controls/mutant1.sh"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}

	// The service never sees the expected actions or the evaluation criteria.
	scen, err := os.ReadFile(filepath.Join(dir, "scenario", "scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"evaluation_criteria", "cancel_booking", "\"250\"", "purpose"} {
		if strings.Contains(string(scen), leak) {
			t.Errorf("scenario.json holds %q:\n%s", leak, scen)
		}
	}
	// Nor does the agent's instruction.
	instr, err := os.ReadFile(filepath.Join(dir, "instruction.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"respond_to_user", "###STOP###", "Fixture Airline Policy"} {
		if !strings.Contains(string(instr), want) {
			t.Errorf("instruction.md lacks %q", want)
		}
	}
	if strings.Contains(string(instr), "cancel_booking") {
		t.Errorf("instruction.md leaks an expected action")
	}
	df, err := os.ReadFile(filepath.Join(dir, "service", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "ARG TAU2_REF="+fixtureRef) || strings.Contains(string(df), "__TAU2_REF__") {
		t.Fatalf("Dockerfile does not pin the commit:\n%s", df)
	}
}

func TestImportSettings(t *testing.T) {
	cases := []struct {
		name      string
		extra     map[string]string
		ids       []string
		limit     int
		wantTasks []string
	}{
		{name: "nl unscored keeps the judged task", extra: map[string]string{SetNL: NLUnscored, SetDomains: "retail"}, wantTasks: []string{"retail-1", "retail-2", "retail-3"}},
		{name: "all tasks of a domain", extra: map[string]string{SetTaskSet: TaskSetAll, SetDomains: "retail"}, wantTasks: []string{"retail-1", "retail-3", "retail-4"}},
		{name: "noop tasks included", extra: map[string]string{SetNoop: NoopInclude, SetDomains: "airline"}, wantTasks: []string{"airline-a1", "airline-a2", "airline-a3", "airline-a5", "airline-a6"}},
		{name: "ids by upstream id", extra: map[string]string{SetDomains: "airline"}, ids: []string{"a3"}, wantTasks: []string{"airline-a3"}},
		{name: "limit", limit: 1, wantTasks: []string{"airline-a1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _ := runImport(t, c.extra, c.ids, c.limit)
			if !slices.Equal(res.Tasks, c.wantTasks) {
				t.Fatalf("tasks = %v, want %v (skipped %+v)", res.Tasks, c.wantTasks, res.Skipped)
			}
		})
	}
}

func TestImportExtendsSuite(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: time.Now}
	opts := importers.Options{BenchRoot: root, From: fixtureDir(t), SuiteID: "tau2-x", Extra: map[string]string{SetRef: fixtureRef, SetDomains: "airline"}}
	if _, err := im.Import(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	opts.Extra = map[string]string{SetRef: fixtureRef, SetDomains: "retail"}
	res, err := im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var s schemas.Suite
	b, err := os.ReadFile(filepath.Join(res.SuiteDir, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if want := []string{"airline-a1", "airline-a3", "airline-a5", "retail-1", "retail-3"}; !slices.Equal(s.TaskIDs, want) {
		t.Fatalf("tasks = %v, want %v", s.TaskIDs, want)
	}
	// A third run without -force leaves existing folders alone.
	res, err = im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 0 || len(res.Skipped) != 3 {
		t.Fatalf("rerun wrote %v, skipped %+v", res.Tasks, res.Skipped)
	}
}

func TestParseSettingsErrors(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]string
		want  string
	}{
		{name: "unknown key", extra: map[string]string{"bogus": "1"}, want: "unknown setting"},
		{name: "telecom", extra: map[string]string{SetDomains: "telecom"}, want: "not supported"},
		{name: "unknown user", extra: map[string]string{SetUser: "human"}, want: "want one of"},
		{name: "bad task set", extra: map[string]string{SetTaskSet: "some"}, want: "want one of"},
		{name: "bad turns", extra: map[string]string{SetMaxTurns: "1"}, want: "from 2 to 200"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseSettings(c.extra)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestImportNeedsCommit(t *testing.T) {
	im := &importer{now: time.Now}
	_, err := im.Import(context.Background(), importers.Options{BenchRoot: t.TempDir(), From: fixtureDir(t), SuiteID: "x"})
	if err == nil || !strings.Contains(err.Error(), "-set ref=") {
		t.Fatalf("err = %v, want a hint about -set ref", err)
	}
}

func TestTaskID(t *testing.T) {
	cases := map[string]string{"0": "airline-0", "12": "airline-12", "A b/C": "airline-a-b-c"}
	for in, want := range cases {
		if got := taskID("airline", in); got != want {
			t.Errorf("taskID(%q) = %q, want %q", in, got, want)
		}
	}
}

func readTaskJSON(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
