// Package python is the issuebench Language for Python repositories tested
// with pytest. It registers itself as "python" on import.
package python

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

func init() { issuebench.Register(Python{}) }

// Minors are the Python 3 minor versions with a slim base image, oldest
// first.
var Minors = []int{8, 9, 10, 11, 12, 13}

// JUnitPath is where TestCommand writes its JUnit XML report unless the
// environment sets BENCH_JUNIT (the SWE-bench verifier does).
const JUnitPath = "/tmp/bench-junit.xml"

// Python implements issuebench.Language.
type Python struct{}

// Name is "python".
func (Python) Name() string { return "python" }

// markers are files at the repository root that mark a Python project.
var markers = []string{"pyproject.toml", "setup.py", "setup.cfg", "tox.ini", "noxfile.py", "Pipfile", "requirements.txt"}

// Detect reports whether repoDir looks like a Python project.
func (Python) Detect(repoDir string) bool {
	for _, m := range markers {
		if isFile(filepath.Join(repoDir, m)) {
			return true
		}
	}
	reqs, _ := filepath.Glob(filepath.Join(repoDir, "requirements*.txt"))
	if len(reqs) > 0 {
		return true
	}
	reqs, _ = filepath.Glob(filepath.Join(repoDir, "requirements", "*.txt"))
	return len(reqs) > 0
}

// IsTestFile reports whether a repository path is test code: anything under
// a tests/ or test/ directory, test_*.py, *_test.py and conftest.py.
func (Python) IsTestFile(p string) bool {
	p = strings.TrimPrefix(strings.ReplaceAll(p, `\`, "/"), "./")
	base := path.Base(p)
	if base == "conftest.py" {
		return true
	}
	if strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) {
		return true
	}
	dirs := strings.Split(path.Dir(p), "/")
	for _, d := range dirs {
		if d == "tests" || d == "test" {
			return true
		}
	}
	return false
}

// BaseImages are the slim Python images, newest first.
func (Python) BaseImages() []string {
	out := make([]string, 0, len(Minors))
	for i := len(Minors) - 1; i >= 0; i-- {
		out = append(out, image(Minors[i]))
	}
	return out
}

func image(minor int) string { return fmt.Sprintf("python:3.%d-slim", minor) }

// PickBaseImages orders the base images for one repository: versions its CI
// tests that requires-python allows come first (newest first), then the
// other allowed versions, newest first. With no allowed version it returns
// all images newest first.
func PickBaseImages(repoDir string) []string {
	spec := requiresPython(repoDir)
	ci := ciMinors(repoDir)
	var first, rest []string
	for i := len(Minors) - 1; i >= 0; i-- {
		m := Minors[i]
		if !allows(spec, m) {
			continue
		}
		if ci[m] {
			first = append(first, image(m))
		} else {
			rest = append(rest, image(m))
		}
	}
	if out := append(first, rest...); len(out) > 0 {
		return out
	}
	return Python{}.BaseImages()
}

// Hints are short facts about how the repository installs and tests itself,
// for the builder agent.
func (Python) Hints(repoDir string) []string {
	var h []string
	if s := requiresPython(repoDir); s != "" {
		h = append(h, "requires-python: "+s)
	}
	if ci := ciMinors(repoDir); len(ci) > 0 {
		var vs []string
		for m := range ci {
			vs = append(vs, fmt.Sprintf("3.%d", m))
		}
		sort.Strings(vs)
		h = append(h, "CI tests Python "+strings.Join(vs, ", "))
	}
	h = append(h, "base images to try in order: "+strings.Join(PickBaseImages(repoDir), ", "))
	for _, m := range markers {
		if isFile(filepath.Join(repoDir, m)) {
			h = append(h, "has "+m)
		}
	}
	reqs, _ := filepath.Glob(filepath.Join(repoDir, "requirements*.txt"))
	more, _ := filepath.Glob(filepath.Join(repoDir, "requirements", "*.txt"))
	for _, r := range append(reqs, more...) {
		rel, _ := filepath.Rel(repoDir, r)
		if rel != "requirements.txt" {
			h = append(h, "requirements file "+filepath.ToSlash(rel))
		}
	}
	if extras := testExtras(readFile(filepath.Join(repoDir, "pyproject.toml")) + "\n" + readFile(filepath.Join(repoDir, "setup.cfg")) + "\n" + readFile(filepath.Join(repoDir, "setup.py"))); len(extras) > 0 {
		h = append(h, "test extras: "+strings.Join(extras, ", ")+" (pip install -e '.["+extras[0]+"]')")
	}
	if pyproject := readFile(filepath.Join(repoDir, "pyproject.toml")); strings.Contains(pyproject, "[tool.pytest") {
		h = append(h, "pytest config in pyproject.toml [tool.pytest.ini_options]")
	}
	for _, f := range []string{"pytest.ini", "tox.ini", "setup.cfg"} {
		if strings.Contains(readFile(filepath.Join(repoDir, f)), "[pytest]") || strings.Contains(readFile(filepath.Join(repoDir, f)), "[tool:pytest]") {
			h = append(h, "pytest config in "+f)
		}
	}
	for _, f := range []string{"uv.lock", "poetry.lock", "pdm.lock", "Pipfile.lock"} {
		if isFile(filepath.Join(repoDir, f)) {
			h = append(h, "lock file "+f)
		}
	}
	h = append(h, ciCommands(repoDir)...)
	return h
}

// PinEnv makes uv resolve packages as they were at the given time
// (UV_EXCLUDE_NEWER), so installs match the issue's date. Use InstallCommand
// for an explicit flag.
func (Python) PinEnv(at time.Time) []string {
	env := []string{"PIP_DISABLE_PIP_VERSION_CHECK=1", "PYTHONDONTWRITEBYTECODE=1"}
	if at.IsZero() {
		return env
	}
	return append(env, "UV_EXCLUDE_NEWER="+pinDate(at))
}

func pinDate(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// InstallCommand is a uv pip install pinned to packages released before at.
func InstallCommand(at time.Time, args ...string) string {
	parts := []string{"uv", "pip", "install"}
	if !at.IsZero() {
		parts = append(parts, "--exclude-newer", pinDate(at))
	}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// TestCommand runs pytest on the given test ids (all tests when empty) with
// the -rA summary in the output and a JUnit XML report at $BENCH_JUNIT (or
// JUnitPath). xunit1 keeps the file attribute so test ids map back to paths.
func (Python) TestCommand(tests []string) string {
	parts := []string{"python", "-m", "pytest", "-rA", "--tb=short", "-p", "no:cacheprovider",
		"-o", "junit_family=xunit1", `--junitxml="${BENCH_JUNIT:-` + JUnitPath + `}"`}
	for _, t := range tests {
		parts = append(parts, shellQuote(t))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s != "" && regexp.MustCompile(`^[A-Za-z0-9_./:=@+-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// readFile returns at most 1 MiB of a file, or "".
func readFile(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	b := make([]byte, 1<<20)
	n, _ := f.Read(b)
	return string(b[:n])
}

var (
	reqPyRe   = regexp.MustCompile(`(?m)^\s*requires-python\s*=\s*["']([^"']+)["']`)
	pyReqsRe  = regexp.MustCompile(`python_requires\s*=\s*["']?([^"'\n,)]+(?:,[^"'\n)]+)*)["']?`)
	clauseRe  = regexp.MustCompile(`^(>=|<=|==|!=|~=|>|<)?\s*(\d+)(?:\.(\d+|\*))?`)
	minorRe   = regexp.MustCompile(`\b3\.(\d{1,2})\b`)
	toxEnvRe  = regexp.MustCompile(`\bpy3(\d{1,2})\b`)
	extraHead = regexp.MustCompile(`(?m)^\s*\[project\.optional-dependencies\]|^\s*\[options\.extras_require\]|extras_require\s*=`)
	extraName = regexp.MustCompile(`(?m)(?:^|[\s{,])["']?([A-Za-z0-9_-]*(?:test|dev)[A-Za-z0-9_-]*)["']?\s*[=:]`)
)

// requiresPython returns the project's Python version specifier, or "".
func requiresPython(repoDir string) string {
	if m := reqPyRe.FindStringSubmatch(readFile(filepath.Join(repoDir, "pyproject.toml"))); m != nil {
		return strings.TrimSpace(m[1])
	}
	for _, f := range []string{"setup.cfg", "setup.py"} {
		if m := pyReqsRe.FindStringSubmatch(readFile(filepath.Join(repoDir, f))); m != nil {
			return strings.TrimSpace(strings.Trim(m[1], `"' `))
		}
	}
	return ""
}

// allows reports whether a requires-python specifier admits Python 3.minor.
// Unknown clauses admit everything.
func allows(spec string, minor int) bool {
	for _, c := range strings.Split(spec, ",") {
		c = strings.TrimSpace(c)
		m := clauseRe.FindStringSubmatch(c)
		if c == "" || m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[2])
		vm := 0
		wild := m[3] == "*" || m[3] == ""
		if !wild {
			vm, _ = strconv.Atoi(m[3])
		}
		cmp := compare(3, minor, major, vm)
		if wild && (m[1] == "" || m[1] == "==" || m[1] == "~=" || m[1] == "!=") {
			// "3", "==3.*": only the major counts.
			cmp = compare(3, 0, major, 0)
		}
		ok := true
		switch m[1] {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "==", "":
			ok = cmp == 0
		case "!=":
			ok = cmp != 0
		case "~=":
			ok = cmp >= 0 && major == 3
		}
		if !ok {
			return false
		}
	}
	return true
}

func compare(a1, a2, b1, b2 int) int {
	switch {
	case a1 != b1:
		return a1 - b1
	default:
		return a2 - b2
	}
}

// ciFiles are the CI and task-runner files read for versions and commands.
func ciFiles(repoDir string) []string {
	files, _ := filepath.Glob(filepath.Join(repoDir, ".github", "workflows", "*.y*ml"))
	for _, f := range []string{".travis.yml", ".gitlab-ci.yml", "azure-pipelines.yml", ".circleci/config.yml", "tox.ini", "noxfile.py"} {
		if isFile(filepath.Join(repoDir, f)) {
			files = append(files, filepath.Join(repoDir, f))
		}
	}
	sort.Strings(files)
	return files
}

// ciMinors returns the Python 3 minor versions CI configuration tests.
func ciMinors(repoDir string) map[int]bool {
	out := map[int]bool{}
	add := func(s string) {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 && n < 40 {
			out[n] = true
		}
	}
	for _, f := range ciFiles(repoDir) {
		text := readFile(f)
		base := filepath.Base(f)
		if base == "tox.ini" {
			for _, m := range toxEnvRe.FindAllStringSubmatch(text, -1) {
				add(m[1])
			}
			continue
		}
		inList := false
		for _, line := range strings.Split(text, "\n") {
			t := strings.TrimSpace(line)
			lower := strings.ToLower(t)
			if inList {
				if strings.HasPrefix(t, "-") {
					for _, m := range minorRe.FindAllStringSubmatch(t, -1) {
						add(m[1])
					}
					continue
				}
				inList = false
			}
			if strings.Contains(lower, "python") {
				if strings.HasSuffix(t, ":") {
					inList = true
				}
				for _, m := range minorRe.FindAllStringSubmatch(t, -1) {
					add(m[1])
				}
			}
		}
	}
	return out
}

var cmdRe = regexp.MustCompile(`(?i)\b(pip install|uv pip install|uv sync|poetry install|pdm install|pytest|python -m pytest|tox|nox|apt-get install)\b`)

// ciCommands returns install and test lines from CI files, at most 12.
func ciCommands(repoDir string) []string {
	var out []string
	seen := map[string]bool{}
	for _, f := range ciFiles(repoDir) {
		rel, _ := filepath.Rel(repoDir, f)
		for _, line := range strings.Split(readFile(f), "\n") {
			t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "- "))
			t = strings.TrimSpace(strings.TrimPrefix(t, "run:"))
			if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") || !cmdRe.MatchString(t) || len(t) > 200 {
				continue
			}
			if seen[t] {
				continue
			}
			seen[t] = true
			out = append(out, "CI ("+filepath.ToSlash(rel)+"): "+t)
			if len(out) >= 12 {
				return out
			}
		}
	}
	return out
}

// testExtras finds optional-dependency groups named like test or dev.
func testExtras(text string) []string {
	loc := extraHead.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	section := text[loc[1]:]
	if end := regexp.MustCompile(`(?m)^\s*\[`).FindStringIndex(section); end != nil && end[0] > 0 {
		section = section[:end[0]]
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range extraName.FindAllStringSubmatch(section, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.Contains(out[i], "test") && !strings.Contains(out[j], "test") })
	return out
}
