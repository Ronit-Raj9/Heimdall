package marble

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
)

const fixtureRev = "1111111111111111111111111111111111111111"

// fixtureClone copies testdata/clone (hand-written records in the upstream
// format) into a temporary folder and gives it a git HEAD.
func fixtureClone(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join("testdata", "clone")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst, ".git", "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, ".git", "refs", "heads", "main"), []byte(fixtureRev+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

func runImport(t *testing.T, root, from, suiteID string, ids []string, extra map[string]string) (*importers.Result, error) {
	t.Helper()
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC) }}
	return im.Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: suiteID, IDs: ids, Extra: extra})
}

func TestImportDatabase(t *testing.T) {
	clone, root := fixtureClone(t), t.TempDir()
	res, err := runImport(t, root, clone, "marble-db", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Tasks, []string{"database-1", "database-2"}) {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	skipped := map[string]string{}
	for _, s := range res.Skipped {
		skipped[s.ID] = s.Reason
	}
	for id, want := range map[string]string{
		"database/3": "anomaly MISSING_INDEXES has no replica",
		"database/4": `relationship 4 names agent "agent9"`,
		"database/5": "root cause MISSING_INDEXES is not one of the task's labels",
		"database/6": "member(s) agent3 have no route to the lead agent1",
		"database/7": `agent id "Agent1" is not a valid team member id`,
	} {
		if !strings.Contains(skipped[id], want) {
			t.Errorf("skip %s = %q, want it to contain %q", id, skipped[id], want)
		}
	}
	notes := strings.Join(res.Notes, "\n")
	for _, want := range []string{"2 task(s) with 3 members", "at least 3 slots", "bargaining: 1 task(s) refused, competitive scenario",
		"minecraft: 1 task(s) refused, needs an external simulator: a Minecraft Java 1.19.2 server", "research: 2 task(s), import with -set scenario=research", "not comparable with the MultiAgentBench paper"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	benchmarks := filepath.Join(root, "benchmarks")
	s, task, err := suite.LoadTask(benchmarks, "marble-db", "database-2")
	if err != nil {
		t.Fatalf("generated suite does not load: %v", err)
	}
	if s.Grading != schemas.GradingVerified || s.Track != schemas.TrackToolUse || s.Grader != "verifier_command" || s.Imported == nil || s.Imported.Importer != Name ||
		s.Imported.License != "MIT" || s.Imported.Revision != fixtureRev || s.Verifier.Result != schemas.VerifierResultReward {
		t.Errorf("suite = %+v imported %+v", s, s.Imported)
	}
	for _, p := range suite.Lint(s) {
		if p.Severity == suite.SeverityError {
			t.Errorf("suite lint error: %+v", p)
		}
	}
	for _, p := range suite.LintTask(s, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("task lint error: %+v", p)
		}
	}
	// The relationship graph is a chain: the team keeps it, the lead first.
	if len(task.Team) != 3 || task.Team[0].ID != "agent1" || task.Team[0].Role != schemas.AgentRoleLead || task.Team[2].Role != schemas.AgentRoleWorker {
		t.Errorf("team = %+v", task.Team)
	}
	if len(task.Services) != 2 || task.Services[0].ID != dbService || !task.Services[0].Hidden || task.Services[0].Image != defaultPostgres ||
		task.Services[1].ID != toolService || task.Services[1].Ready == nil || task.Services[1].Ready.HTTP != "/healthz" {
		t.Errorf("services = %+v", task.Services)
	}
	if task.Output == nil || task.Output.Kind != schemas.OutputArtifact || len(task.Output.Artifacts) != 1 || !task.Output.Artifacts[0].Required {
		t.Errorf("output = %+v", task.Output)
	}
	if task.Provenance == nil || task.Provenance.BaseCommit != fixtureRev || !strings.Contains(task.Provenance.ImportedFrom, "database_main.jsonl line 2 (task_id 2, sha256 ") {
		t.Errorf("provenance = %+v", task.Provenance)
	}
	dir := filepath.Join(benchmarks, "marble-db", "tasks", "database-2")
	var exp struct {
		RootCauses []string `json:"root_causes"`
		MaxLabels  int      `json:"max_labels"`
		Routes     routes   `json:"routes"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "verifier", "tests", "expected.json"))
	if err != nil || json.Unmarshal(b, &exp) != nil {
		t.Fatalf("expected.json: %v", err)
	}
	if exp.MaxLabels != 3 || !slices.Equal(exp.RootCauses, []string{"VACUUM", "LOCK_CONTENTION"}) || slices.Contains(exp.Routes.Allowed["agent1"], "agent3") {
		t.Errorf("expected = %+v", exp)
	}
	ins, _ := os.ReadFile(filepath.Join(dir, "instruction.md"))
	for _, want := range []string{"- `agent1`: `agent2`\n", "- `agent3`: `agent2`\n", "ROOT_CAUSES: <label>, <label>", "naming at most 3 of these labels", "HEIMDALL_AGENT_ID` is not set, you are working alone", "/s/dbtool"} {
		if !strings.Contains(string(ins), want) {
			t.Errorf("instruction lacks %q", want)
		}
	}
	mut, _ := os.ReadFile(filepath.Join(dir, "controls", "mutant-missed-cause.sh"))
	if !strings.Contains(string(mut), "ROOT_CAUSES: VACUUM, POOR_JOIN_PERFORMANCE,CPU_CONTENTION") {
		t.Errorf("mutant = %s", mut)
	}
	for _, f := range []string{"LICENSE.marble", "NOTICE.md", ".gitignore", "experiments/equal-budget.yaml"} {
		if _, err := os.Stat(filepath.Join(benchmarks, "marble-db", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	lic, _ := os.ReadFile(filepath.Join(benchmarks, "marble-db", "LICENSE.marble"))
	if !strings.Contains(string(lic), "fixture copy") {
		t.Errorf("LICENSE.marble is not the clone's LICENSE: %s", lic)
	}
	ex, _ := os.ReadFile(filepath.Join(benchmarks, "marble-db", "experiments", "equal-budget.yaml"))
	for _, want := range []string{"fairness: equal_budget", "topology: single", "topology: team", "topology: best_of_n", "agent: agents/noop.yaml", `suite: "marble-db@1.0.0"`, `tasks: ["database-1", "database-2"]`} {
		if !strings.Contains(string(ex), want) {
			t.Errorf("experiment lacks %q:\n%s", want, ex)
		}
	}
	// An experiment file the planner accepts: decode it like `plan` does.
	var e schemas.Experiment
	exPath := filepath.Join(benchmarks, "marble-db", "experiments", "equal-budget.yaml")
	if err := suite.DecodeYAMLFile(exPath, exPath, suite.SchemaExperiment, &e); err != nil || len(e.Arms) != 4 || e.Arms["A"].Set["topology"] != "single" {
		t.Errorf("experiment decode: %v %+v", err, e.Arms)
	}
}

func TestImportSelectionAndForce(t *testing.T) {
	clone, root := fixtureClone(t), t.TempDir()
	res, err := runImport(t, root, clone, "marble-db", []string{"2", "database-1", "99"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Tasks, []string{"database-1", "database-2"}) || len(res.Skipped) != 1 || res.Skipped[0].ID != "99" {
		t.Fatalf("tasks %v skipped %+v", res.Tasks, res.Skipped)
	}
	res, err = runImport(t, root, clone, "marble-db", []string{"1"}, nil)
	if err != nil || len(res.Tasks) != 0 || !strings.Contains(res.Skipped[0].Reason, "use -force") {
		t.Fatalf("second import: %v %+v", err, res)
	}
	// Another scenario never goes into this suite.
	if _, err := runImport(t, root, clone, "marble-db", nil, map[string]string{setScenario: scenarioResearch}); err == nil || !strings.Contains(err.Error(), "holds another MultiAgentBench scenario") {
		t.Errorf("mixed scenarios: %v", err)
	}
	// A single file works without a clone, but then names no revision.
	res, err = runImport(t, t.TempDir(), filepath.Join(clone, "multiagentbench", "database", "database_main.jsonl"), "one", []string{"1"}, nil)
	if err != nil || len(res.Tasks) != 1 || !strings.Contains(strings.Join(res.Notes, "\n"), "revision is unknown") {
		t.Errorf("single file: %v %+v", err, res)
	}
}

func TestImportJudged(t *testing.T) {
	clone, root := fixtureClone(t), t.TempDir()
	refs := t.TempDir()
	if err := os.WriteFile(filepath.Join(refs, "research-1.answer"), []byte("**[Question 1]** my reference\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := runImport(t, root, clone, "marble-research", nil, map[string]string{setScenario: scenarioResearch, setReferences: refs})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Tasks, []string{"research-1", "research-2"}) {
		t.Fatalf("tasks = %v", res.Tasks)
	}
	benchmarks := filepath.Join(root, "benchmarks")
	s, task, err := suite.LoadTask(benchmarks, "marble-research", "research-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Grading != schemas.GradingJudged || s.Track != schemas.TrackQA || s.Grader != "judge" || s.Judge == nil || s.Judge.Provider != placeholder || s.Output == nil || s.Output.AnswerPath != "proposal.md" {
		t.Errorf("suite = %+v judge %+v output %+v", s, s.Judge, s.Output)
	}
	if len(task.Team) != 3 {
		t.Errorf("team = %+v", task.Team)
	}
	for _, p := range suite.LintTask(s, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("task lint error: %+v", p)
		}
	}
	dir := filepath.Join(benchmarks, "marble-research", "tasks")
	if b, _ := os.ReadFile(filepath.Join(dir, "research-1", "solution", "reference.answer")); string(b) != "**[Question 1]** my reference\n" {
		t.Errorf("reference from -set references = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "research-2", "solution", "reference.answer")); !strings.Contains(string(b), "ships none") {
		t.Errorf("missing reference = %q", b)
	}
	_, solo, err := suite.LoadTask(benchmarks, "marble-research", "research-2")
	if err != nil || len(solo.Team) != 0 {
		t.Errorf("a one-agent task has no team: %v %+v", err, solo)
	}
	ans, _ := os.ReadFile(filepath.Join(dir, "research-1", "verifier", "answer.yaml"))
	if !strings.Contains(string(ans), "innovation") || !strings.Contains(string(ans), "wrong_answers") {
		t.Errorf("answer.yaml = %s", ans)
	}
	notes := strings.Join(res.Notes, "\n")
	if !strings.Contains(notes, "No judged MARBLE task has been admitted here") || !strings.Contains(notes, "pin judge.provider") {
		t.Errorf("notes = %s", notes)
	}
	res, err = runImport(t, root, clone, "marble-coding", nil, map[string]string{setScenario: scenarioCoding})
	if err != nil || !slices.Equal(res.Tasks, []string{"coding-1"}) {
		t.Fatalf("coding: %v %+v", err, res)
	}
	cs, _, err := suite.LoadTask(benchmarks, "marble-coding", "coding-1")
	if err != nil || cs.Output.AnswerPath != "solution.py" {
		t.Errorf("coding suite: %v %+v", err, cs)
	}
}

func TestParseSettingsRefusals(t *testing.T) {
	for _, tc := range []struct {
		extra map[string]string
		want  string
	}{
		{map[string]string{setScenario: scenarioMinecraft}, "not imported: needs an external simulator: a Minecraft Java 1.19.2 server"},
		{map[string]string{setScenario: scenarioBargaining}, "not imported: competitive scenario"},
		{map[string]string{setScenario: scenarioWerewolf}, "not imported: competitive social-deduction game"},
		{map[string]string{setScenario: "chess"}, "unknown scenario"},
		{map[string]string{"colour": "red"}, "unknown -set colour"},
		{map[string]string{setVersion: "1.0"}, "want MAJOR.MINOR.PATCH"},
		{map[string]string{setAgentS: "5"}, "between 60 and 14400"},
		{map[string]string{setReferences: "/tmp"}, "applies to the judged scenarios"},
		{map[string]string{setPostgres: "a b"}, "want an image reference"},
	} {
		if _, err := parseSettings(tc.extra); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.extra, err, tc.want)
		}
	}
	s, err := parseSettings(map[string]string{setLead: "agent2", setAgentS: "600", setPostgres: "postgres:17"})
	if err != nil || s.scenario != scenarioDatabase || s.lead != "agent2" || s.agentS != 600 || s.postgres != "postgres:17" {
		t.Errorf("settings = %+v, %v", s, err)
	}
}

func TestImportSourceErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := runImport(t, root, t.TempDir(), "x", nil, nil); err == nil || !strings.Contains(err.Error(), "no multiagentbench/ folder") {
		t.Errorf("not a clone: %v", err)
	}
	clone := fixtureClone(t)
	if err := os.RemoveAll(filepath.Join(clone, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(t, root, clone, "x", nil, nil); err == nil || !strings.Contains(err.Error(), "-set revision=<commit>") {
		t.Errorf("no git: %v", err)
	}
	if _, err := runImport(t, root, clone, "x", []string{"1"}, map[string]string{setRevision: fixtureRev}); err != nil {
		t.Errorf("-set revision: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{\"scenario\": \"database\"}\nnot json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(t, root, bad, "y", nil, nil); err == nil || !strings.Contains(err.Error(), "bad.jsonl line 2") {
		t.Errorf("bad line: %v", err)
	}
}

func TestInfoRegistered(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("marble is not registered")
	}
	info := im.Info()
	if info.License != "MIT" || !strings.Contains(info.Source, upstreamRepo) || len(info.Needs) == 0 {
		t.Errorf("info = %+v", info)
	}
}

// TestImportRealClone imports every database, research and coding task of a
// real MARBLE clone (MARBLE_DIR) and lints each one. MARBLE_REVISION names
// the commit when MARBLE_DIR is not a git clone.
func TestImportRealClone(t *testing.T) {
	from := os.Getenv("MARBLE_DIR")
	if from == "" {
		t.Skip("MARBLE_DIR not set")
	}
	for _, sc := range []string{scenarioDatabase, scenarioResearch, scenarioCoding} {
		root := t.TempDir()
		res, err := (&importer{now: time.Now}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "marble-" + sc,
			Extra: map[string]string{setScenario: sc, setRevision: os.Getenv("MARBLE_REVISION")}})
		if err != nil {
			t.Fatalf("%s: %v", sc, err)
		}
		t.Logf("%s: %d tasks, %d skipped", sc, len(res.Tasks), len(res.Skipped))
		for _, s := range res.Skipped {
			t.Logf("skip %s: %s", s.ID, s.Reason)
		}
		for _, id := range res.Tasks {
			s, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "marble-"+sc, id)
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			for _, p := range suite.LintTask(s, task) {
				if p.Severity == suite.SeverityError {
					t.Errorf("%s: lint error: %+v", id, p)
				}
			}
		}
	}
}

func TestImportNoRecordsOfScenario(t *testing.T) {
	clone := fixtureClone(t)
	file := filepath.Join(clone, "multiagentbench", "research", "research_main.jsonl")
	if _, err := runImport(t, t.TempDir(), file, "x", nil, nil); err == nil || !strings.Contains(err.Error(), "no database tasks in") {
		t.Errorf("research file, database scenario: %v", err)
	}
	if err := os.Remove(filepath.Join(clone, "multiagentbench", "coding", "coding_main.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(t, t.TempDir(), clone, "x", nil, map[string]string{setScenario: scenarioCoding}); err == nil || !strings.Contains(err.Error(), "coding_main.jsonl") {
		t.Errorf("clone without the file: %v", err)
	}
}

func TestReimportKeepsPinnedJudge(t *testing.T) {
	clone, root := fixtureClone(t), t.TempDir()
	extra := map[string]string{setScenario: scenarioResearch}
	if _, err := runImport(t, root, clone, "r", []string{"1"}, extra); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "benchmarks", "r", "suite.yaml")
	b, _ := os.ReadFile(p)
	pinned := strings.Replace(strings.Replace(string(b), "provider: \"pin-me\"", "provider: anthropic", 1), "model: \"pin-me\"", "model: claude-sonnet-4-5", 1)
	if pinned == string(b) {
		t.Fatalf("placeholder judge not found in:\n%s", b)
	}
	if err := os.WriteFile(p, []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runImport(t, root, clone, "r", []string{"2"}, extra); err != nil {
		t.Fatal(err)
	}
	s, _, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "r", "research-2")
	if err != nil || s.Judge == nil || s.Judge.Provider != "anthropic" || s.Judge.Model != "claude-sonnet-4-5" || len(s.TaskIDs) != 2 {
		t.Errorf("judge after re-import: %v %+v tasks %v", err, s.Judge, s.TaskIDs)
	}
}
