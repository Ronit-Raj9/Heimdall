package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// writeFiles writes path -> content under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLockUnchangedForExistingSuites re-locks every committed pack and
// requires its suite.lock byte for byte: scenario hashing must not move the
// lock hash of a suite written before scenarios.
func TestLockUnchangedForExistingSuites(t *testing.T) {
	packs := []struct{ root, ref string }{
		{filepath.Join("..", "benchmarks"), "coding-core"},
		{filepath.Join("..", "examples", "acme-node", "benchmarks"), "acme-orders"},
	}
	for _, p := range packs {
		t.Run(p.ref, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(p.root, p.ref, LockFile))
			if err != nil {
				t.Skipf("lock not readable: %v", err)
			}
			s, err := newLocalLoader(p.root).Load(context.Background(), p.ref)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			l, err := Lock(s, fixedNow)
			if err != nil {
				t.Fatalf("lock: %v", err)
			}
			got, err := encodeLock(l)
			if err != nil {
				t.Fatal(err)
			}
			// Lock fingerprints: a committed lock may carry the additive
			// fingerprints section; the legacy fields must still re-lock
			// byte for byte, and the section must match the files.
			if !bytes.Equal(got, legacyLockBytes(t, want)) {
				t.Errorf("re-locking %s changed suite.lock", p.ref)
			}
			checkCommittedFingerprints(t, s)
		})
	}
}

// legacyLockBytes is a committed lock encoded without its fingerprints
// section: the bytes the legacy lock fields alone produce.
func legacyLockBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	var l schemas.SuiteLock
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		t.Fatal(err)
	}
	l.Fingerprints = nil
	out, err := encodeLock(&l)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// checkCommittedFingerprints recomputes a committed lock's fingerprints
// without docker and requires no drift but the part versions, which this
// package cannot know (the planner test binds them): every setting and file
// of the suite, its tasks and probes is what the lock recorded, on any
// machine. Image ids are not checked without docker.
func checkCommittedFingerprints(t *testing.T, s *schemas.Suite) {
	t.Helper()
	if s.Lock == nil || s.Lock.Fingerprints == nil {
		return
	}
	drift, _, err := CheckFingerprints(s, FingerprintOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range drift {
		if d.Scope == ScopeSuite && d.Part == PartSettings && reflect.DeepEqual(d.Fields, []string{"parts"}) {
			continue
		}
		t.Errorf("committed lock fingerprints drifted: %s", DriftText([]schemas.FingerprintDrift{d}))
	}
}

// scenarioTaskYAML is m01 with every scenario field set.
const scenarioTaskYAML = `
environment:
  dockerfile: env/Dockerfile
  workspace_path: /app
services:
  - id: db
    role: data
    image: python:3.12-slim
    argv: ["python", "-m", "http.server", "8000"]
    port: 8000
    files:
      /data/seed.json: services/seed.json
    ready:
      http: /
    export:
      - id: dump
        argv: ["cat", "/data/seed.json"]
sessions:
  - id: s1
    instruction: sessions/one.md
    files: sessions/files1
  - id: s2
    instruction: sessions/two.md
output:
  kind: state
skills: skills
impossible: true
`

func scenarioFiles() map[string]string {
	return map[string]string{
		"env/Dockerfile":         "FROM python:3.12-slim\n",
		"services/seed.json":     "{}\n",
		"sessions/one.md":        "Remember the number 7.\n",
		"sessions/two.md":        "What was the number?\n",
		"sessions/files1/a.txt":  "seven\n",
		"skills/math/SKILL.md":   "# math\n",
		"controls/reference2.sh": "#!/bin/sh\ntrue\n",
	}
}

func TestLoadScenarioFields(t *testing.T) {
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, scenarioFiles())
	editFile(t, filepath.Join(task, TaskFile), "timeouts:", strings.TrimPrefix(scenarioTaskYAML, "\n")+"timeouts:")
	editFile(t, filepath.Join(pack, SuiteFile), "tasks: [m01]", `track: tool_use
grading: verified
output:
  kind: diff
services:
  - id: cache
    image: redis:7
judge:
  provider: anthropic
  model: m
  prompt_version: v1
imported:
  importer: harbor
  benchmark: tb@2
  source: https://example.invalid/tb
  license: Apache-2.0
  at: 2026-11-15T00:00:00Z
tasks: [m01]`)
	editFile(t, filepath.Join(pack, SuiteFile), "timeout_s: 60", "timeout_s: 60\n  mode: snapshot")
	s := loadMini(t, benchRoot)
	tk := s.Tasks[0]
	if tk.Environment == nil || tk.Environment.WorkspacePath != "/app" || len(tk.Services) != 1 || len(tk.Sessions) != 2 ||
		tk.Output == nil || tk.Output.Kind != schemas.OutputState || tk.Skills != "skills" || !tk.Impossible {
		t.Fatalf("task scenario fields not decoded: %+v", tk)
	}
	if s.Track != schemas.TrackToolUse || s.Judge == nil || s.Imported == nil || len(s.Services) != 1 || s.Verifier.Mode != schemas.VerifierSnapshot {
		t.Fatalf("suite scenario fields not decoded: %+v", s)
	}

	if err := os.Remove(filepath.Join(task, "sessions", "two.md")); err != nil {
		t.Fatal(err)
	}
	_, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err == nil || !strings.Contains(err.Error(), "sessions[1].instruction") {
		t.Fatalf("missing session instruction: got %v", err)
	}
}

// TestLockFoldsScenarioContent checks each scenario input moves the task
// lock hash of its change level, and that removing it restores the hash.
func TestLockFoldsScenarioContent(t *testing.T) {
	benchRoot, pack := copyPack(t)
	base, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	before := base.Tasks["m01"]
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, scenarioFiles())
	orig, err := os.ReadFile(filepath.Join(task, TaskFile))
	if err != nil {
		t.Fatal(err)
	}
	editFile(t, filepath.Join(task, TaskFile), "timeouts:", strings.TrimPrefix(scenarioTaskYAML, "\n")+"timeouts:")
	l, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	after := l.Tasks["m01"]
	for name, pair := range map[string][2]string{
		"instruction": {before.InstructionSHA256, after.InstructionSHA256},
		"repo":        {before.RepoTreeSHA256, after.RepoTreeSHA256},
		"verifier":    {before.VerifierSHA256, after.VerifierSHA256},
		"fixtures":    {before.FixturesSHA256, after.FixturesSHA256},
	} {
		if pair[0] == pair[1] {
			t.Errorf("%s hash did not change with scenario content", name)
		}
	}
	if after.ControlsSHA256 != before.ControlsSHA256 {
		t.Errorf("controls hash changed although no control did")
	}

	// A session file change moves only the instruction hash.
	writeFiles(t, task, map[string]string{"sessions/files1/a.txt": "eight\n"})
	l2, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := l2.Tasks["m01"]; got.InstructionSHA256 == after.InstructionSHA256 || got.FixturesSHA256 != after.FixturesSHA256 {
		t.Errorf("session file change: instruction %v, fixtures %v", got.InstructionSHA256 != after.InstructionSHA256, got.FixturesSHA256 != after.FixturesSHA256)
	}

	// Without the scenario fields the task hashes exactly as before.
	if err := os.WriteFile(filepath.Join(task, TaskFile), orig, 0o644); err != nil {
		t.Fatal(err)
	}
	l3, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if got := l3.Tasks["m01"]; got.InstructionSHA256 != before.InstructionSHA256 || got.RepoTreeSHA256 != before.RepoTreeSHA256 ||
		got.VerifierSHA256 != before.VerifierSHA256 || got.FixturesSHA256 != before.FixturesSHA256 {
		t.Errorf("hashes differ after removing scenario fields: %+v vs %+v", got, before)
	}
}

func TestFoldHash(t *testing.T) {
	if got := foldHash("abc", nil); got != "abc" {
		t.Errorf("empty fold changed the hash: %s", got)
	}
	a := foldHash("abc", map[string]string{"x": "1", "y": "2"})
	b := foldHash("abc", map[string]string{"y": "2", "x": "1"})
	if a != b || a == "abc" {
		t.Errorf("fold not order independent or did not change: %s %s", a, b)
	}
}

func TestLintScenario(t *testing.T) {
	no := false
	tests := []struct {
		name    string
		edit    func(s *schemas.Suite, tk *schemas.Task)
		wantMsg string
		wantSev string // default error
	}{
		{"judged without judge", func(s *schemas.Suite, _ *schemas.Task) { s.Grading = schemas.GradingJudged }, "grading: judged needs a judge", ""},
		{"judge grader in verified suite", func(s *schemas.Suite, _ *schemas.Task) { s.Grader = "judge" }, "must declare grading: judged", ""},
		{"even votes", func(s *schemas.Suite, _ *schemas.Task) {
			s.Grading = schemas.GradingJudged
			s.Judge = &schemas.JudgeSpec{Provider: "p", Model: "m", PromptVersion: "v", Votes: 2}
		}, "votes: 2 is even", ""},
		{"answer with hidden_tests", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
		}, "output: answer needs grader answer_match, judge or verifier_command", ""},
		{"answer_match without answer file", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "answer_match"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
		}, "needs the hidden answer", ""},
		{"state without export", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x"}}
		}, "output: state needs at least one service export", ""},
		{"duplicate service id", func(s *schemas.Suite, tk *schemas.Task) {
			s.Services = []schemas.ServiceSpec{{ID: "db", Image: "x"}}
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "y"}}
		}, `"db" is already a service in suite.yaml`, ""},
		{"toolproxy reserved", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "toolproxy", Image: "x"}}
		}, "reserved for the bench's tool proxy", ""},
		{"bad host name", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "My_DB", Image: "x"}}
		}, "is not a valid host name", ""},
		{"no image", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db"}}
		}, "needs image or build", ""},
		{"ready both", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Port: 1, Ready: &schemas.ReadyCheck{Argv: []string{"true"}, HTTP: "/"}}}
		}, "set exactly one of argv or http", ""},
		{"ready neither", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Ready: &schemas.ReadyCheck{}}}
		}, "set exactly one of argv or http", ""},
		{"ready http without port", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Ready: &schemas.ReadyCheck{HTTP: "/"}}}
		}, "needs the service's port", ""},
		{"service file escapes", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Proxy: &no, Files: map[string]string{"/a": "../../suite.yaml"}}}
		}, "services[0].files[/a]", ""},
		{"export both", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Export: []schemas.StateExport{{ID: "a", Argv: []string{"cat"}, Path: "/a"}}}}
		}, "export[0]: set exactly one of argv or path", ""},
		{"duplicate session", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Sessions = []schemas.SessionSpec{{ID: "s", Instruction: "instruction.md"}, {ID: "s", Instruction: "instruction.md"}}
		}, `sessions[1].id: "s" is used by an earlier session`, ""},
		{"session instruction missing", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Sessions = []schemas.SessionSpec{{ID: "s", Instruction: "nope.md"}}
		}, "sessions[0].instruction: nope.md not found", ""},
		{"session path escapes", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Sessions = []schemas.SessionSpec{{ID: "s", Instruction: "../../suite.yaml"}}
		}, "sessions[0].instruction", ""},
		{"environment empty", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Environment = &schemas.TaskEnvironment{WorkspacePath: "/app"}
		}, "environment: needs image or dockerfile", ""},
		{"script mutant not script", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Controls.Reference = "controls/exploit.sh"
		}, "controls.mutants[0]", ""},
		{"patch reference on state task", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
			tk.Services = []schemas.ServiceSpec{{ID: "db", Image: "x", Export: []schemas.StateExport{{ID: "a", Path: "/a"}}}}
		}, "usually needs a script reference", SeverityWarning},
		{"artifact with hidden_tests", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "report.csv"}}}
		}, "output: artifact files are read by verifier_command (from /grader/artifacts), not by hidden_tests", ""},
		{"suite artifact with hidden_tests", func(s *schemas.Suite, _ *schemas.Task) {
			s.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "report", Path: "report.csv"}}}
		}, "output: artifact files are read by verifier_command", ""},
		{"artifact without artifacts", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact}
		}, "output: artifact needs at least one artifact", ""},
		{"artifact id used twice", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "a.csv"}, {ID: "r", Path: "b.csv"}}}
		}, `output.artifacts[1].id: "r" is used by an earlier artifact`, ""},
		{"artifact path escapes", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "../secret.csv"}}}
		}, "output.artifacts[0]: artifact r: path \"../secret.csv\" must be clean and stay inside the workspace", ""},
		{"artifact path absolute", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "/workspace/out.csv"}}}
		}, "must be relative to the workspace root", ""},
		{"artifact bad id", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "Report", Path: "r.csv"}}}
		}, `artifact id "Report" is not`, ""},
		{"too many artifacts", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			var specs []schemas.ArtifactSpec
			for i := 0; i <= utils.MaxArtifactSpecs; i++ {
				specs = append(specs, schemas.ArtifactSpec{ID: fmt.Sprintf("a%d", i), Path: fmt.Sprintf("a%d.txt", i)})
			}
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: specs}
		}, fmt.Sprintf("output.artifacts: %d declared, at most %d", utils.MaxArtifactSpecs+1, utils.MaxArtifactSpecs), ""},
		{"suite artifact path escapes", func(s *schemas.Suite, _ *schemas.Task) {
			s.Grader = "verifier_command"
			s.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "a/../../b"}}}
		}, "output.artifacts[0]: artifact r", ""},
		{"artifact patch reference", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
		}, "controls.reference: solution/patch.diff is not a script; an artifact task's controls run in the agent's place", ""},
		{"artifact patch empty control", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
			tk.Controls.Reference = "controls/exploit.sh"
		}, "controls.empty:", ""},
		{"artifact patch mutant", func(s *schemas.Suite, tk *schemas.Task) {
			s.Grader = "verifier_command"
			tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
			tk.Controls.Reference = "controls/exploit.sh"
		}, "controls.mutants[0]:", ""},
		{"artifacts under another kind", func(_ *schemas.Suite, tk *schemas.Task) {
			tk.Output = &schemas.OutputSpec{Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
		}, "output.artifacts: declared, but output kind is diff", SeverityWarning},
		{"suite artifacts under another kind", func(s *schemas.Suite, _ *schemas.Task) {
			s.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer, Artifacts: []schemas.ArtifactSpec{{ID: "r", Path: "r.csv"}}}
		}, "output.artifacts: declared, but output kind is answer", SeverityWarning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, _ := copyPack(t)
			s := loadMini(t, benchRoot)
			tc.edit(s, s.Tasks[0])
			want := tc.wantSev
			if want == "" {
				want = SeverityError
			}
			for _, p := range Lint(s) {
				if strings.Contains(p.Message, tc.wantMsg) && p.Severity == want {
					return
				}
			}
			t.Errorf("no %s problem containing %q in %+v", want, tc.wantMsg, Lint(s))
		})
	}
}

func TestLintScenarioCleanAnswerTask(t *testing.T) {
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, map[string]string{"verifier/tests/answer.yaml": "match: exact\nexpected: [\"7\"]\n", "controls/answer.txt": "7\n"})
	s := loadMini(t, benchRoot)
	s.Grader = "answer_match"
	s.ProbeIDs, s.Probes = nil, nil
	tk := s.Tasks[0]
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputAnswer}
	tk.Controls.Reference = "controls/answer.txt"
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("unexpected error: %s: %s", p.Path, p.Message)
		}
	}
	for _, p := range ValidateTask(context.Background(), s, tk) {
		if strings.Contains(p.Message, "does not apply") {
			t.Errorf("answer control checked as a patch: %s", p.Message)
		}
	}
}

func TestKnownLayersIncludeScenarioLayers(t *testing.T) {
	if !knownLayers[schemas.LayerTool] || !knownLayers[schemas.LayerService] {
		t.Error("tool and service probe layers are not known to lint")
	}
}

// TestScenarioFixture loads the hand-written scenario pack the end-to-end
// tests run (testdata/scenario): it lints without errors and locks.
func TestScenarioFixture(t *testing.T) {
	s, err := newLocalLoader(filepath.Join("testdata", "scenario")).Load(context.Background(), "hd-run-e2e")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("lint error: %s: %s", p.Path, p.Message)
		}
	}
	l, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Tasks) != 2 {
		t.Fatalf("lock tasks %v", l.Tasks)
	}
	state, recall := s.TaskByID("state"), s.TaskByID("recall")
	if k := utils.ControlKind(s, state); k != utils.ControlScript {
		t.Errorf("state control kind %s", k)
	}
	if k := utils.ControlKind(s, recall); k != utils.ControlAnswer {
		t.Errorf("recall control kind %s", k)
	}
}

// TestExperimentSetScenarioKeys checks that an arm may set the agent's
// skills, mcp, memory and remote sections, as the Harbor importer's skill
// comparison does.
func TestExperimentSetScenarioKeys(t *testing.T) {
	doc := `
name: skills
suite: s@1.0.0
base: agents/codex.yaml
arms:
  A: {label: without skills, set: {skills.enabled: false, mcp.format: env, memory.paths: [/m], remote.timeout_s: 30}}
  B: {label: with skills, set: {skills.enabled: true}}
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: all, trials: 2}
seed: 1
`
	var e schemas.Experiment
	if err := DecodeYAML([]byte(doc), "experiment.yaml", SchemaExperiment, &e); err != nil {
		t.Fatalf("experiment with scenario arm keys refused: %v", err)
	}
	bad := strings.Replace(doc, "remote.timeout_s", "remotes.timeout_s", 1)
	if err := DecodeYAML([]byte(bad), "experiment.yaml", SchemaExperiment, &e); err == nil {
		t.Error("an unknown arm key was accepted")
	}
	if b, err := os.ReadFile(filepath.Join("..", "benchmarks", "hd-harbor-skills", "experiment-skills.yaml")); err == nil {
		if err := DecodeYAML(b, "experiment-skills.yaml", SchemaExperiment, &schemas.Experiment{}); err != nil {
			t.Errorf("harbor experiment-skills.yaml: %v", err)
		}
	}
}

func TestLintVisibleTestsInImage(t *testing.T) {
	benchRoot, pack := copyPack(t)
	if err := os.Remove(filepath.Join(pack, "tasks", "m01", "repo", "tests", "test_core.py")); err != nil {
		t.Fatal(err)
	}
	s := loadMini(t, benchRoot)
	has := func() bool {
		for _, p := range Lint(s) {
			if strings.Contains(p.Message, "no visible tests") {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("a repo without tests was not reported")
	}
	s.Tasks[0].Environment = &schemas.TaskEnvironment{Image: "x:1", WorkspacePath: "/testbed"}
	if has() {
		t.Error("no visible tests reported for a repo that lives in the task image")
	}
}

// TestLockFoldsServiceCode checks that the source folder services/<id>/ of a
// service (in the suite folder, or the task folder for a task service) moves
// the task's fixtures hash, and that a folder of an id no service uses does
// not.
func TestLockFoldsServiceCode(t *testing.T) {
	benchRoot, pack := copyPack(t)
	lockOf := func(edit func(s *schemas.Suite)) schemas.TaskLock {
		t.Helper()
		s := loadMini(t, benchRoot)
		edit(s)
		l, err := Lock(s, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		return l.Tasks["m01"]
	}
	none := func(*schemas.Suite) {}
	base := lockOf(none)
	writeFiles(t, pack, map[string]string{"services/desk/server.py": "v1\n", "services/other/server.py": "x\n"})
	if got := lockOf(none); got.FixturesSHA256 != base.FixturesSHA256 {
		t.Fatal("service folders changed the hash of a task without services")
	}
	withDesk := func(s *schemas.Suite) {
		s.Tasks[0].Services = []schemas.ServiceSpec{{ID: "desk", Image: "acme/desk:1"}}
	}
	v1 := lockOf(withDesk)
	writeFiles(t, pack, map[string]string{"services/other/server.py": "y\n"})
	if got := lockOf(withDesk); got.FixturesSHA256 != v1.FixturesSHA256 {
		t.Error("an unused service folder changed the hash")
	}
	writeFiles(t, pack, map[string]string{"services/desk/server.py": "v2\n"})
	v2 := lockOf(withDesk)
	if v2.FixturesSHA256 == v1.FixturesSHA256 {
		t.Error("editing the suite's services/desk did not change the lock")
	}
	writeFiles(t, filepath.Join(pack, "tasks", "m01"), map[string]string{"services/desk/extra.py": "1\n"})
	if got := lockOf(withDesk); got.FixturesSHA256 == v2.FixturesSHA256 {
		t.Error("the task's services/desk did not change the lock")
	}
}

// TestLockArtifactOutput: an output spec without artifacts hashes as it did
// before the artifacts field existed, and declaring or changing an artifact
// moves only the verifier hash.
func TestLockArtifactOutput(t *testing.T) {
	sum, err := utils.HashJSON(&schemas.OutputSpec{Kind: schemas.OutputState})
	if err != nil {
		t.Fatal(err)
	}
	if want := utils.SHA256Bytes([]byte(`{"kind":"state"}`)); sum != want {
		b, _ := utils.CanonicalJSON(&schemas.OutputSpec{Kind: schemas.OutputState})
		t.Errorf("output spec without artifacts hashes differently: %s", b)
	}
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	lockWith := func(output string) *schemas.SuiteLock {
		t.Helper()
		orig, err := os.ReadFile(filepath.Join(task, TaskFile))
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.WriteFile(filepath.Join(task, TaskFile), orig, 0o644); err != nil {
				t.Fatal(err)
			}
		}()
		editFile(t, filepath.Join(task, TaskFile), "timeouts:", output+"timeouts:")
		l, err := Lock(loadMini(t, benchRoot), fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	plain := lockWith("output:\n  kind: artifact\n").Tasks["m01"]
	one := lockWith("output:\n  kind: artifact\n  artifacts:\n    - {id: report, path: report.csv, required: true}\n").Tasks["m01"]
	other := lockWith("output:\n  kind: artifact\n  artifacts:\n    - {id: report, path: out/report.csv, required: true}\n").Tasks["m01"]
	if plain.VerifierSHA256 == one.VerifierSHA256 || one.VerifierSHA256 == other.VerifierSHA256 {
		t.Errorf("artifacts did not change the verifier hash: %s %s %s", plain.VerifierSHA256, one.VerifierSHA256, other.VerifierSHA256)
	}
	if plain.InstructionSHA256 != one.InstructionSHA256 || plain.RepoTreeSHA256 != one.RepoTreeSHA256 || plain.ControlsSHA256 != one.ControlsSHA256 {
		t.Errorf("artifacts changed a hash other than the verifier's")
	}
}

func TestLintScenarioCleanArtifactTask(t *testing.T) {
	benchRoot, pack := copyPack(t)
	writeFiles(t, filepath.Join(pack, "tasks", "m01"), map[string]string{
		"controls/ref.sh":   "#!/bin/sh\necho total > report.csv\n",
		"controls/empty.sh": "#!/bin/sh\nexit 0\n",
		"controls/m1.sh":    "#!/bin/sh\necho wrong > report.csv\n",
	})
	s := loadMini(t, benchRoot)
	s.Grader = "verifier_command"
	s.ProbeIDs, s.Probes = nil, nil
	tk := s.Tasks[0]
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputArtifact, Artifacts: []schemas.ArtifactSpec{
		{ID: "report", Path: "report.csv", Required: true, MaxBytes: 1 << 20},
		{ID: "charts", Path: "charts/*.svg"},
	}}
	tk.Controls = schemas.TaskControls{Reference: "controls/ref.sh", Empty: "controls/empty.sh", Mutants: []string{"controls/m1.sh"}, Exploit: "controls/exploit.sh"}
	for _, p := range Lint(s) {
		if p.Severity == SeverityError || strings.Contains(p.Message, "artifact") {
			t.Errorf("unexpected problem: %s: %s: %s", p.Severity, p.Path, p.Message)
		}
	}
	// The same artifacts declared once in suite.yaml serve every task.
	s.Output, tk.Output = tk.Output, &schemas.OutputSpec{Kind: schemas.OutputArtifact}
	for _, p := range Lint(s) {
		if p.Severity == SeverityError {
			t.Errorf("suite-level artifacts: unexpected error: %s: %s", p.Path, p.Message)
		}
	}
}
