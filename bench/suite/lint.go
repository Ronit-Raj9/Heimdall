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
	"github.com/maximhq/bifrost/bench/utils"
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
	schemas.LayerTool:     true,
	schemas.LayerService:  true,
	schemas.LayerAdapter:  true,
	schemas.LayerProcess:  true, // crash-resume (probes/process.go)
	schemas.LayerA2A:      true, // B1: client-side A2A stream faults (probes/a2a.go)
	schemas.LayerCompound: true, // X22: fault schedules across the gateway and tool layers (probes/compound.go)
}

// leakTerms are words an instruction must not contain: naming the hidden
// tests, the grader or the notes files tells the agent how it is measured.
var leakTerms = []string{"hidden test", "grader", "agents.md", "claude.md"}

var testFuncRE = regexp.MustCompile(`(?m)^[ \t]*(?:async[ \t]+)?def[ \t]+(test_\w+)[ \t]*\(`)

// EditMeMarker is the line `task init` writes at the top of a draft
// instruction.md. A task cannot be validated or admitted while it is there.
const EditMeMarker = "<!-- EDIT ME -->"

// staleNotesProbe is the probe that needs every task's notes.stale.
const staleNotesProbe = "stale-notes"

// Lint reports problems in a loaded suite that the JSON Schemas cannot see:
// duplicate ids, probes on a layer the bench does not ship, and every task
// problem LintTask finds. Problems are sorted by path.
func Lint(s *schemas.Suite) []Problem {
	out := lintSuite(s)
	linted := map[string]bool{}
	for _, t := range s.Tasks {
		if linted[t.ID] {
			continue
		}
		linted[t.ID] = true
		out = append(out, LintTask(s, t)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// lintSuite is the part of Lint that concerns the suite itself, not a task.
func lintSuite(s *schemas.Suite) []Problem {
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
			add(rel(s.Root, p.Path), SeverityError, "layer: %q is not a probe layer the bench ships (fixture, gateway, tool_shim, tool, service, adapter, a2a)", p.Layer)
		}
	}
	out = append(out, lintScenarioSuite(s)...)
	return out
}

// LintTask checks one task folder of s: stale notes when the suite runs the
// stale-notes probe, mutants, instructions that leak how grading works or
// still hold the EDIT ME marker, requirements maps that miss a hidden test,
// required test ids that do not exist or that the suite's result format
// ignores, hidden test files that are also in the repo, repos without
// visible tests, and symlinks in the task folder (an error when one resolves
// outside it, a warning otherwise). Mutants and a requirements map are
// recommended, not required (docs/bring-your-own-design.md).
func LintTask(s *schemas.Suite, t *schemas.Task) []Problem {
	root := s.Root
	var out []Problem
	taskYAML := rel(root, filepath.Join(t.Dir, TaskFile))
	add := func(path, sev, format string, args ...any) {
		out = append(out, Problem{Path: path, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	switch {
	case t.Notes.Stale == "" && slices.Contains(s.ProbeIDs, staleNotesProbe):
		add(taskYAML, SeverityError, "notes.stale: missing; the suite runs the stale-notes probe, which needs stale notes for every task")
	case t.Notes.Stale != "" && len(t.Notes.StaleClaims) == 0:
		add(taskYAML, SeverityWarning, "notes.stale_claims: empty; the report cannot say what the stale notes get wrong")
	}
	if len(t.Controls.Mutants) == 0 {
		add(taskYAML, SeverityWarning, "controls.mutants: empty; a mutant that fails the hidden tests shows they catch a near miss")
	}
	out = append(out, lintInstruction(root, t)...)
	out = append(out, lintScenarioTask(s, t)...)
	out = append(out, lintGPUTask(s, t)...)   // X7: GPU requests (gpu.go)
	out = append(out, lintWorldTask(s, t)...) // world clock (world.go)
	out = append(out, lintChecksTask(s, t)...) // declarative checks.yaml (checkslint.go)
	output := utils.OutputKind(s, t)

	testsDir := filepath.Join(t.Dir, filepath.FromSlash(t.Verifier.Tests))
	funcs, err := hiddenTests(testsDir)
	if err != nil {
		add(taskYAML, SeverityError, "verifier.tests: %v", err)
	}
	if output != schemas.OutputAnswer {
		out = append(out, lintRequirements(root, t, funcs)...)
	}
	out = append(out, lintRequired(root, t, testsDir)...)
	if r := s.Verifier.Result; len(t.Verifier.Required) > 0 && (r == schemas.VerifierResultReward || r == schemas.VerifierResultExitCode) {
		add(taskYAML, SeverityWarning, "verifier.required: ignored, because the suite's verifier reports result %s; only a junit report names the tests that ran", r)
	}
	out = append(out, lintEditMe(root, testsDir)...)
	out = append(out, lintSymlinks(root, t)...)

	repo := filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir))
	out = append(out, lintLeakedTests(root, repo, testsDir)...)
	if n, err := countVisibleTests(repo); err != nil {
		add(taskYAML, SeverityError, "repo_dir: %v", err)
	} else if n == 0 && output == schemas.OutputDiff && !repoInImage(t) {
		sev := SeverityWarning
		if s.Grader == "hidden_tests" {
			sev = SeverityError
		}
		add(rel(root, repo), sev, "no visible tests (test_*.py, *_test.py, *.test.js, *_test.go, ...) in the repo")
	}
	return out
}

// lintInstruction checks the instruction for words that reveal how the work
// is measured and for the EDIT ME marker of a draft.
func lintInstruction(root string, t *schemas.Task) []Problem {
	instrPath := filepath.Join(t.Dir, filepath.FromSlash(t.Instruction))
	b, err := os.ReadFile(instrPath)
	if err != nil {
		return []Problem{{Path: rel(root, instrPath), Severity: SeverityError, Message: fmt.Sprintf("instruction: %v", err)}}
	}
	var out []Problem
	lower := strings.ToLower(string(b))
	for _, term := range leakTerms {
		if strings.Contains(lower, term) {
			out = append(out, Problem{Path: rel(root, instrPath), Severity: SeverityError,
				Message: fmt.Sprintf("mentions %q; the instruction must not reveal grading or notes files", term)})
		}
	}
	if n := editMeLine(b); n > 0 {
		out = append(out, Problem{Path: rel(root, instrPath), Severity: SeverityError,
			Message: fmt.Sprintf("line %d: still holds the %s marker; rewrite the draft and delete the marker", n, EditMeMarker)})
	}
	return out
}

// lintSymlinks reports every symlink in the task folder whose real target
// lies outside it (an error: the loader refuses such a task too) and lists
// the ones that stay inside in one warning. Inside links are allowed: the
// workspace and the grader copy them as links, never what they point to.
func lintSymlinks(root string, t *schemas.Task) []Problem {
	links, err := utils.FindSymlinks(t.Dir, t.Dir)
	if err != nil {
		return []Problem{{Path: rel(root, t.Dir), Severity: SeverityError, Message: err.Error()}}
	}
	var out []Problem
	var inside []string
	for _, l := range links {
		if l.Escapes {
			out = append(out, Problem{Path: rel(root, filepath.Join(t.Dir, filepath.FromSlash(l.Path))), Severity: SeverityError,
				Message: fmt.Sprintf("symlink -> %s resolves outside the task folder; replace it with the file it points to, or remove it", l.Target)})
			continue
		}
		inside = append(inside, l.Path+" -> "+l.Target)
	}
	if len(inside) > 0 {
		shown := inside
		if len(shown) > maxListedLinks {
			shown = append(shown[:maxListedLinks:maxListedLinks], fmt.Sprintf("and %d more", len(inside)-maxListedLinks))
		}
		out = append(out, Problem{Path: rel(root, t.Dir), Severity: SeverityWarning,
			Message: fmt.Sprintf("%d symlinks stay inside the task folder (%s); they are copied as links, check that the task needs them", len(inside), strings.Join(shown, ", "))})
	}
	return out
}

// maxListedLinks is how many inside symlinks one lint warning names.
const maxListedLinks = 5

// lintEditMe reports verifier files that still hold an "EDIT ME" line, such
// as the test.sh stub of a generic-reward task.
func lintEditMe(root, testsDir string) []Problem {
	var out []Problem
	err := filepath.WalkDir(testsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if n := editMeLine(b); n > 0 {
			out = append(out, Problem{Path: rel(root, path), Severity: SeverityError,
				Message: fmt.Sprintf("line %d: still holds an EDIT ME line; finish the file and delete the line", n)})
		}
		return nil
	})
	if err != nil {
		out = append(out, Problem{Path: rel(root, testsDir), Severity: SeverityError, Message: fmt.Sprintf("scan for EDIT ME: %v", err)})
	}
	return out
}

// editMeLine returns the 1-based number of the first line that is an EDIT ME
// marker ("<!-- EDIT ME -->", or a comment starting "# EDIT ME"), or 0.
func editMeLine(b []byte) int {
	for i, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, EditMeMarker) || strings.HasPrefix(l, "# EDIT ME") {
			return i + 1
		}
	}
	return 0
}

// lintLeakedTests reports hidden test files whose exact content is also in
// the repo, where the agent could read it. Support files (conftest.py,
// fixtures, helpers) may be shared; only test files count.
func lintLeakedTests(root, repo, testsDir string) []Problem {
	hidden, err := utils.HashFiles(testsDir)
	if err != nil {
		return nil
	}
	visible, err := utils.HashFiles(repo)
	if err != nil {
		return nil
	}
	bySum := map[string]string{}
	for p, sum := range visible {
		bySum[sum] = p
	}
	var out []Problem
	for _, p := range sortedKeys(hidden) {
		if !isTestFile(p) || hidden[p] == emptySHA256 {
			continue
		}
		if vp, ok := bySum[hidden[p]]; ok {
			out = append(out, Problem{Path: rel(root, filepath.Join(testsDir, filepath.FromSlash(p))), Severity: SeverityError,
				Message: fmt.Sprintf("the same file is visible in the repo as %s; a hidden test must not be readable by the agent", vp)})
		}
	}
	return out
}

// lintRequirements checks that verifier/requirements.md, when present, names
// every hidden pytest function, so each hidden assertion is traceable to the
// instruction.
func lintRequirements(root string, t *schemas.Task, funcs map[string][]string) []Problem {
	reqPath := filepath.Join(t.Dir, "verifier", "requirements.md")
	b, err := os.ReadFile(reqPath)
	if err != nil {
		return []Problem{{Path: rel(root, reqPath), Severity: SeverityWarning, Message: "missing; a map from every hidden test to the instruction sentence it checks keeps the tests honest"}}
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

// lintRequired checks that every pytest verifier.required id
// (file.py::test) names a test file in the hidden tests folder that defines
// the named function, and that every Go id (<import path>::TestName) names a
// test function some hidden _test.go file defines. A required id that cannot
// run makes every grade invalid. Other ids (node's test::<name>) cannot be
// checked statically.
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
		if !strings.HasSuffix(file, ".py") {
			if strings.HasPrefix(fn, "Test") && !goTestDefined(testsDir, fn) {
				out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("verifier.required[%d]: %s: no func %s in a _test.go file of %s", i, id, fn, t.Verifier.Tests)})
			}
			continue
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

// goTestDefined reports whether a _test.go file under dir defines func name.
func goTestDefined(dir, name string) bool {
	re := regexp.MustCompile(`(?m)^func\s+` + regexp.QuoteMeta(name) + `\s*\(`)
	found := false
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found || d.IsDir() || !strings.HasSuffix(d.Name(), "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found = re.Match(b)
		return nil
	})
	return err == nil && found
}

// isTestFile reports whether a slash-separated path is a test file of one of
// the languages the suite templates know: test_*.py, *_test.py, *_test.go,
// and JavaScript or TypeScript files named *.test.*, *-test.*, *_test.*,
// test-* or kept under a test, tests or __tests__ folder.
func isTestFile(p string) bool {
	parts := strings.Split(p, "/")
	base := parts[len(parts)-1]
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch ext {
	case ".py":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(stem, "_test")
	case ".go":
		return strings.HasSuffix(stem, "_test")
	case ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		if strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, "-test") || strings.HasSuffix(stem, "_test") || strings.HasPrefix(stem, "test-") {
			return true
		}
		return slices.ContainsFunc(parts[:len(parts)-1], func(d string) bool { return d == "test" || d == "tests" || d == "__tests__" })
	}
	return false
}

// countVisibleTests counts test files in the repo.
func countVisibleTests(repo string) (int, error) {
	n := 0
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "__pycache__" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if isTestFile(rel(repo, path)) {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan visible tests: %w", err)
	}
	return n, nil
}

// emptySHA256 is the sha256 of no bytes; empty files are never a leak.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
