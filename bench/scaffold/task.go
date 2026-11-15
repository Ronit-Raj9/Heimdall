package scaffold

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// EditMeMarker is the line task init puts at the top of instruction.md (and,
// for generic-reward tasks, in verifier/tests/test.sh). `task validate` and
// admission fail while a file still holds it.
const EditMeMarker = "<!-- EDIT ME -->"

// Task kinds and difficulties accepted by spec/task.schema.json.
var (
	taskKinds    = []string{"bugfix", "feature", "refactor", "performance"}
	difficulties = []string{"easy", "medium", "hard"}
)

// defaultTestGlobs are the -tests globs task init uses when none are given.
var defaultTestGlobs = map[string][]string{
	TemplatePythonPytest:  {"tests/**", "test_*.py", "*_test.py"},
	TemplateNodeTest:      {"test/**", "**/*.test.js", "**/*.test.mjs", "**/*.test.ts"},
	TemplateGoTest:        {"**/*_test.go"},
	TemplateGenericReward: {"tests/**"},
}

// DefaultTestGlobs returns the -tests globs task init uses for a suite
// template, or nil for an unknown template.
func DefaultTestGlobs(tmpl string) []string {
	return slices.Clone(defaultTestGlobs[tmpl])
}

// MatchGlob reports whether the slash-separated repository path name matches
// pattern. A pattern without a "/" matches the base name at any depth
// ("test_*.py"); otherwise it is anchored at the repository root, "**"
// matches any number of folders ("tests/**", "**/*_test.go"), and every other
// segment follows path.Match.
func MatchGlob(pattern, name string) bool {
	pattern = strings.TrimPrefix(pattern, "./")
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "/") {
		ok, err := path.Match(pattern, path.Base(name))
		return err == nil && ok
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// MatchAny reports whether name matches one of patterns.
func MatchAny(patterns []string, name string) bool {
	for _, p := range patterns {
		if MatchGlob(p, name) {
			return true
		}
	}
	return false
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return len(name) > 0
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(rest, name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

var templateCommentRE = regexp.MustCompile(`(?m)^#\s*template:\s*([a-z0-9-]+)\s*$`)

// DetectTemplate names the suite template a suite.yaml was written from: the
// "# template: <name>" comment `suite init` writes, otherwise a guess from
// the track (scenario suites), the grader and the verifier command. It
// returns "" when it cannot tell.
func DetectTemplate(suiteYAML []byte) string {
	if m := templateCommentRE.FindSubmatch(suiteYAML); m != nil {
		if _, ok := defaultTestGlobs[string(m[1])]; ok || IsScenarioTemplate(string(m[1])) {
			return string(m[1])
		}
	}
	var s struct {
		Grader   string `yaml:"grader"`
		Track    string `yaml:"track"`
		Verifier struct {
			Command []string `yaml:"command"`
			Result  string   `yaml:"result"`
		} `yaml:"verifier"`
	}
	if err := yaml.Unmarshal(suiteYAML, &s); err != nil {
		return ""
	}
	cmd := strings.Join(s.Verifier.Command, " ")
	switch s.Track {
	case "mcp":
		return TemplateMCP
	case "tool_use":
		return TemplateToolUse
	case "memory":
		return TemplateMemory
	case "qa":
		return TemplateQA
	}
	switch {
	case s.Grader == graderHiddenTests || strings.Contains(cmd, "pytest"):
		return TemplatePythonPytest
	case strings.Contains(cmd, "go-junit") || strings.Contains(cmd, "go test"):
		return TemplateGoTest
	case strings.Contains(cmd, "node"):
		return TemplateNodeTest
	case s.Verifier.Result == "reward":
		return TemplateGenericReward
	}
	return ""
}

// IsTestFile reports whether the repository path p is a file the template's
// test runner executes (as opposed to a fixture or helper next to it).
func IsTestFile(tmpl, p string) bool {
	base := path.Base(p)
	switch tmpl {
	case TemplatePythonPytest:
		return strings.HasSuffix(base, ".py") && (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py"))
	case TemplateGoTest:
		return strings.HasSuffix(base, "_test.go")
	case TemplateNodeTest:
		if !hasAnySuffix(base, ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts") {
			return false
		}
		stem := base[:strings.LastIndexByte(base, '.')]
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, "-test") || strings.HasSuffix(stem, "_test") ||
			strings.HasPrefix(stem, "test-") || slices.ContainsFunc(strings.Split(path.Dir(p), "/"), func(d string) bool {
			return d == "test" || d == "tests" || d == "__tests__"
		})
	}
	return false
}

// AddTaskID adds id to the tasks list of a suite.yaml with the smallest
// textual edit, so comments, order and layout survive: inside a flow list
// ("tasks: [t01]"), as a new item after the last one of a block list, after
// an empty "tasks:" key, or as a new "tasks: [id]" line when the key is
// missing. It reports false when id is already listed. The result is parsed
// again and must list exactly the old tasks plus id.
func AddTaskID(src []byte, id string) ([]byte, bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, false, fmt.Errorf("parse suite.yaml: %w", err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false, fmt.Errorf("suite.yaml is not a mapping")
	}
	m := doc.Content[0]
	var key, val *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "tasks" {
			key, val = m.Content[i], m.Content[i+1]
		}
	}
	old := scalarList(val)
	if slices.Contains(old, id) {
		return src, false, nil
	}
	out, err := insertTaskID(src, key, val, id, len(old))
	if err != nil {
		return nil, false, err
	}
	var check struct {
		Tasks []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(out, &check); err != nil || !slices.Equal(check.Tasks, append(slices.Clone(old), id)) {
		return nil, false, fmt.Errorf("could not add %q to the tasks list of suite.yaml; add it by hand", id)
	}
	return out, true, nil
}

func insertTaskID(src []byte, key, val *yaml.Node, id string, n int) ([]byte, error) {
	switch {
	case key == nil:
		out := slices.Clone(src)
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		return append(out, []byte("tasks: ["+id+"]\n")...), nil
	case val.Kind == yaml.ScalarNode && val.Tag == "!!null":
		if val.Value == "" {
			at := offset(src, key.Line, key.Column) + len("tasks:")
			return splice(src, at, at, " ["+id+"]"), nil
		}
		at := offset(src, val.Line, val.Column)
		return splice(src, at, at+len(val.Value), "["+id+"]"), nil
	case val.Kind == yaml.SequenceNode && val.Style&yaml.FlowStyle != 0:
		open := offset(src, val.Line, val.Column)
		closing := matchingBracket(src, open)
		if closing < 0 {
			return nil, fmt.Errorf("suite.yaml: unterminated tasks list")
		}
		last := closing - 1
		for last > open && unicode.IsSpace(rune(src[last])) {
			last--
		}
		switch {
		case n == 0:
			return splice(src, open+1, closing, id), nil
		case src[last] == ',':
			return splice(src, last+1, last+1, " "+id+","), nil
		default:
			return splice(src, last+1, last+1, ", "+id), nil
		}
	case val.Kind == yaml.SequenceNode && len(val.Content) > 0:
		item := val.Content[len(val.Content)-1]
		start := offset(src, item.Line, 1)
		end := bytes.IndexByte(src[start:], '\n')
		if end < 0 {
			src = append(slices.Clone(src), '\n')
			end = len(src) - 1 - start
		}
		prefix := string(src[start:offset(src, item.Line, item.Column)])
		return splice(src, start+end+1, start+end+1, prefix+id+"\n"), nil
	}
	return nil, fmt.Errorf("suite.yaml: tasks is not a list")
}

// offset turns a 1-based line and column into a byte offset.
func offset(src []byte, line, col int) int {
	at := 0
	for l := 1; l < line; l++ {
		i := bytes.IndexByte(src[at:], '\n')
		if i < 0 {
			return len(src)
		}
		at += i + 1
	}
	for c := 1; c < col && at < len(src); c++ {
		_, size := utf8.DecodeRune(src[at:])
		at += size
	}
	return at
}

// matchingBracket returns the offset of the "]" closing the "[" at open,
// skipping quoted strings and comments, or -1.
func matchingBracket(src []byte, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; {
		case c == '\'' || c == '"':
			j := i + 1
			for j < len(src) && (src[j] != c || (c == '"' && src[j-1] == '\\')) {
				j++
			}
			i = j
		case c == '#' && i > 0 && unicode.IsSpace(rune(src[i-1])):
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '[':
			depth++
		case c == ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splice(src []byte, from, to int, s string) []byte {
	out := make([]byte, 0, len(src)+len(s))
	out = append(out, src[:from]...)
	out = append(out, s...)
	return append(out, src[to:]...)
}

func scalarList(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, c.Value)
	}
	return out
}

var (
	pyClassRE = regexp.MustCompile(`^class\s+(Test\w*)\s*[:(]`)
	pyDefRE   = regexp.MustCompile(`^([ \t]*)(?:async[ \t]+)?def[ \t]+(test\w*)[ \t]*\(`)
	goTestRE  = regexp.MustCompile(`(?m)^func\s+(Test[^\s(]*)\s*\(\s*\w+\s+\*testing\.T\s*\)`)
	goModRE   = regexp.MustCompile(`(?m)^module\s+"?([^\s"]+)"?`)
)

// PythonTestIDs lists the pytest ids ("rel::test_x", "rel::TestC::test_y")
// of the test functions in one test file, rel being its path under
// verifier/tests. Methods count only at the first indentation of a Test*
// class body; nested functions are not tests.
func PythonTestIDs(rel string, src []byte) []string {
	var ids []string
	class, classIndent := "", ""
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := pyClassRE.FindStringSubmatch(line); m != nil {
			class, classIndent = m[1], ""
			continue
		}
		if line != "" && !unicode.IsSpace(rune(line[0])) && line[0] != '#' && line[0] != '@' && line[0] != ')' {
			class = ""
		}
		m := pyDefRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		indent, name := m[1], m[2]
		switch {
		case indent == "":
			ids = append(ids, rel+"::"+name)
		case class != "" && (classIndent == "" || indent == classIndent):
			classIndent = indent
			ids = append(ids, rel+"::"+class+"::"+name)
		}
	}
	return dedupe(ids)
}

// GoTestIDs lists the ids ("<import path>::TestName") of the top-level test
// functions in one _test.go file, as the go-test template's go-junit names
// them.
func GoTestIDs(importPath string, src []byte) []string {
	var ids []string
	for _, m := range goTestRE.FindAllSubmatch(src, -1) {
		name := string(m[1])
		if r, _ := utf8.DecodeRuneInString(name[len("Test"):]); len(name) > len("Test") && unicode.IsLower(r) {
			continue
		}
		ids = append(ids, importPath+"::"+name)
	}
	return dedupe(ids)
}

// GoModulePath returns the module path a go.mod declares, or "".
func GoModulePath(gomod []byte) string {
	if m := goModRE.FindSubmatch(gomod); m != nil {
		return string(m[1])
	}
	return ""
}

// trailerRE matches commit trailers that say nothing about the task.
var trailerRE = regexp.MustCompile(`(?i)^(signed-off-by|co-authored-by|change-id|reviewed-by|reviewed-on):`)

// SplitMessage splits a commit message into its subject line and body,
// dropping sign-off and review trailers.
func SplitMessage(msg string) (string, string) {
	lines := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	var kept []string
	for _, l := range lines {
		if !trailerRE.MatchString(strings.TrimSpace(l)) {
			kept = append(kept, strings.TrimRight(l, " \t"))
		}
	}
	text := strings.TrimSpace(strings.Join(kept, "\n"))
	subject, body, _ := strings.Cut(text, "\n")
	return strings.TrimSpace(subject), strings.TrimSpace(body)
}

// SanitizeSource drops credentials from a remote URL before it is written
// into task.yaml. Paths and scp-style remotes pass through unchanged.
func SanitizeSource(remote string) string {
	remote = strings.TrimSpace(remote)
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return remote
	}
	u.User = nil
	return u.String()
}

// TaskMeta is what task.yaml records.
type TaskMeta struct {
	ID, Title, Project, Kind string
	Required, Protected      []string
	Capabilities             []string
	Difficulty               string
	Source, License          string
	Base, Reference          string // full commit ids
}

// TaskYAML renders task.yaml. Kind defaults to bugfix and License to
// proprietary.
func TaskYAML(m TaskMeta) ([]byte, error) {
	if !ValidID(m.ID) {
		return nil, fmt.Errorf("task id %q: want lowercase letters, digits, '.', '_' or '-'", m.ID)
	}
	if m.Kind == "" {
		m.Kind = "bugfix"
	}
	if !slices.Contains(taskKinds, m.Kind) {
		return nil, fmt.Errorf("kind %q: want one of %s", m.Kind, strings.Join(taskKinds, ", "))
	}
	if m.Difficulty != "" && !slices.Contains(difficulties, m.Difficulty) {
		return nil, fmt.Errorf("difficulty %q: want one of %s", m.Difficulty, strings.Join(difficulties, ", "))
	}
	if m.License == "" {
		m.License = "proprietary"
	}
	if strings.TrimSpace(m.Title) == "" {
		m.Title = "Task " + m.ID
	}
	if strings.TrimSpace(m.Project) == "" {
		m.Project = m.ID
	}
	raw, err := templates.ReadFile("templates/task/task.yaml.tmpl")
	if err != nil {
		return nil, err
	}
	return render("task.yaml", string(raw), map[string]any{
		"ID": m.ID, "Title": truncate(m.Title, 120), "Project": m.Project, "Kind": m.Kind,
		"Required": m.Required, "Protected": m.Protected, "Capabilities": m.Capabilities,
		"Difficulty": m.Difficulty, "Source": m.Source, "License": m.License,
		"Base": m.Base, "Reference": m.Reference, "BaseShort": shortSHA(m.Base), "RefShort": shortSHA(m.Reference),
	})
}

// Instruction renders the first draft of instruction.md from the reference
// commit's message, under the EDIT ME marker.
func Instruction(subject, body, reference string) ([]byte, error) {
	raw, err := templates.ReadFile("templates/task/instruction.md.tmpl")
	if err != nil {
		return nil, err
	}
	if subject == "" {
		subject = "Describe the task"
	}
	return render("instruction.md", string(raw), map[string]any{
		"Subject": subject, "Body": body, "RefShort": shortSHA(reference),
	})
}

// TaskControls returns the generated files of a task besides task.yaml,
// instruction.md and the repository snapshot: controls/empty.diff, the
// template's generic controls/exploit.sh, and its verifier support files
// (verifier/tests/conftest.py for python-pytest, verifier/tests/test.sh for
// generic-reward).
func TaskControls(tmpl string) ([]File, error) {
	if _, ok := defaultTestGlobs[tmpl]; !ok {
		return nil, fmt.Errorf("unknown suite template %q", tmpl)
	}
	files, err := renderTree("templates/task/"+tmpl, nil)
	if err != nil {
		return nil, err
	}
	out := []File{{Path: "controls/empty.diff", Mode: 0o644}}
	for _, f := range files {
		switch f.Path {
		case "exploit.sh":
			f.Path = "controls/exploit.sh"
		default:
			f.Path = "verifier/tests/" + f.Path
		}
		out = append(out, f)
	}
	sortFiles(out)
	return out, nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
