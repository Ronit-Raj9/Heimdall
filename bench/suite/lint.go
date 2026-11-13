package suite

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Problem severities.
const (
	SeverityError   = "error"   // the task cannot be admitted as written
	SeverityWarning = "warning" // allowed, but worth a look
)

// Problem is one finding of Lint. Path is relative to the suite root.
type Problem struct {
	Path     string `json:"path"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// knownLayers are the probe layers the bench ships (DESIGN.md 8.3).
var knownLayers = map[schemas.ProbeLayerName]bool{
	schemas.LayerFixture:  true,
	schemas.LayerGateway:  true,
	schemas.LayerToolShim: true,
}

// leakTerms are words an instruction must not contain: naming the hidden
// tests, the grader or the notes files tells the agent how it is measured.
var leakTerms = []string{"hidden test", "grader", "agents.md", "claude.md"}

var testFuncRE = regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?def[ \t]+(test_\w+)[ \t]*\(`)

// Lint reports problems in a loaded suite that the JSON Schemas cannot see:
// duplicate ids, probes on a layer the bench does not ship, tasks without
// stale notes or mutants, instructions that leak how grading works,
// requirements maps that miss a hidden test, required test ids that do not
// exist, and repos without visible tests. Problems are sorted by path.
func Lint(s *schemas.Suite) []Problem {
	var out []Problem
	add := func(path, sev, format string, args ...any) {
		out = append(out, Problem{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	for _, id := range duplicates(s.TaskIDs) {
		add(SuiteFile, SeverityError, "tasks: id %q listed more than once", id)
	}
	for _, id := range duplicates(s.ProbeIDs) {
		add(SuiteFile, SeverityError, "probes: id %q listed more than once", id)
	}
	for _, id := range s.TaskIDs {
		if slices.Contains(s.ProbeIDs, id) {
			add(SuiteFile, SeverityError, "id %q is both a task and a probe", id)
		}
	}
	if slices.Contains(s.ProbeIDs, schemas.ConditionClean) {
		add(SuiteFile, SeverityError, "probes: %q is reserved for the unperturbed condition", schemas.ConditionClean)
	}
	for _, p := range s.Probes {
		if !knownLayers[p.Layer] {
			add(rel(s.Root, p.Path), SeverityError, "layer: %q is not a probe layer the bench ships (fixture, gateway, tool_shim)", p.Layer)
		}
	}
	linted := map[string]bool{}
	for _, t := range s.Tasks {
		if linted[t.ID] {
			continue
		}
		linted[t.ID] = true
		out = append(out, lintTask(s.Root, t)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// lintTask checks one task folder.
func lintTask(root string, t *schemas.Task) []Problem {
	var out []Problem
	taskYAML := rel(root, filepath.Join(t.Dir, TaskFile))
	add := func(path, sev, format string, args ...any) {
		out = append(out, Problem{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if t.Notes.Stale == "" {
		add(taskYAML, SeverityError, "notes.stale: missing; every task needs stale notes for the stale-notes probe")
	} else if len(t.Notes.StaleClaims) == 0 {
		add(taskYAML, SeverityWarning, "notes.stale_claims: empty; the report cannot say what the stale notes get wrong")
	}
	if len(t.Controls.Mutants) == 0 {
		add(taskYAML, SeverityError, "controls.mutants: empty; admission needs at least one mutant that fails the hidden tests")
	}

	instrPath := filepath.Join(t.Dir, filepath.FromSlash(t.Instruction))
	if b, err := os.ReadFile(instrPath); err != nil {
		add(rel(root, instrPath), SeverityError, "instruction: %v", err)
	} else {
		lower := strings.ToLower(string(b))
		for _, term := range leakTerms {
			if strings.Contains(lower, term) {
				add(rel(root, instrPath), SeverityError, "mentions %q; the instruction must not reveal grading or notes files", term)
			}
		}
	}

	testsDir := filepath.Join(t.Dir, filepath.FromSlash(t.Verifier.Tests))
	funcs, err := hiddenTests(testsDir)
	if err != nil {
		add(taskYAML, SeverityError, "verifier.tests: %v", err)
	}
	out = append(out, lintRequirements(root, t, funcs)...)
	out = append(out, lintRequired(root, t, testsDir)...)

	repo := filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir))
	if n, err := countVisibleTests(repo); err != nil {
		add(taskYAML, SeverityError, "repo_dir: %v", err)
	} else if n == 0 {
		add(rel(root, repo), SeverityError, "no visible tests (test_*.py or *_test.py) in the repo")
	}
	return out
}

// lintRequirements checks that verifier/requirements.md names every hidden
// test function, so each hidden assertion is traceable to the instruction.
func lintRequirements(root string, t *schemas.Task, funcs map[string][]string) []Problem {
	reqPath := filepath.Join(t.Dir, "verifier", "requirements.md")
	b, err := os.ReadFile(reqPath)
	if err != nil {
		return []Problem{{Path: rel(root, reqPath), Severity: SeverityError, Message: "missing; it must map every hidden test to the instruction sentence it checks"}}
	}
	text := string(b)
	var out []Problem
	for _, name := range sortedFuncNames(funcs) {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(text) {
			out = append(out, Problem{
				Path:     rel(root, reqPath),
				Severity: SeverityError,
				Message:  fmt.Sprintf("does not mention hidden test %s (%s)", name, strings.Join(funcs[name], ", ")),
			})
		}
	}
	return out
}

// lintRequired checks that every verifier.required id names a test file in
// the hidden tests folder that defines the named function. A required id that
// cannot run makes every grade invalid.
func lintRequired(root string, t *schemas.Task, testsDir string) []Problem {
	taskYAML := rel(root, filepath.Join(t.Dir, TaskFile))
	var out []Problem
	for i, id := range t.Verifier.Required {
		parts := strings.Split(id, "::")
		file := parts[0]
		fn := parts[len(parts)-1]
		if k := strings.IndexByte(fn, '['); k >= 0 {
			fn = fn[:k]
		}
		b, err := os.ReadFile(filepath.Join(testsDir, filepath.FromSlash(file)))
		if err != nil {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("verifier.required[%d]: %s: file %s not found in %s", i, id, file, t.Verifier.Tests)})
			continue
		}
		if !regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?def[ \t]+` + regexp.QuoteMeta(fn) + `[ \t]*\(`).Match(b) {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("verifier.required[%d]: %s: no function %s in %s", i, id, fn, file)})
		}
	}
	return out
}

// hiddenTests returns every test function name defined in test_*.py under
// dir, with the files that define it.
func hiddenTests(dir string) (map[string][]string, error) {
	funcs := map[string][]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() || !strings.HasPrefix(d.Name(), "test_") || !strings.HasSuffix(d.Name(), ".py") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := rel(dir, path)
		for _, m := range testFuncRE.FindAllSubmatch(b, -1) {
			fn := string(m[1])
			if !slices.Contains(funcs[fn], name) {
				funcs[fn] = append(funcs[fn], name)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan hidden tests: %w", err)
	}
	return funcs, nil
}

// countVisibleTests counts pytest-style test files in the repo.
func countVisibleTests(repo string) (int, error) {
	n := 0
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".py") && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")) {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan visible tests: %w", err)
	}
	return n, nil
}

func sortedFuncNames(funcs map[string][]string) []string {
	names := make([]string, 0, len(funcs))
	for n := range funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func duplicates(ids []string) []string {
	seen := map[string]int{}
	var dups []string
	for _, id := range ids {
		seen[id]++
		if seen[id] == 2 {
			dups = append(dups, id)
		}
	}
	return dups
}

func rel(base, path string) string {
	r, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(r)
}
