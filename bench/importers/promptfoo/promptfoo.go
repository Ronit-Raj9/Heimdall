// Package promptfoo imports the test cases of a promptfoo config
// (promptfooconfig.yaml: prompts, tests with vars and assert, defaultTest)
// into a Heimdall question-and-answer suite. OpenAI points OpenAI Evals
// users to promptfoo; this importer is the second half of that migration.
//
// Each (prompt, test) pair becomes one answer task: the prompt with the
// test's {{vars}} filled in is the question. Assertions map to checks:
//
//   - equals: the whole answer, regex ^\s*<value>\s*$
//   - starts-with: regex ^\s*<value>
//   - contains / icontains: regex <value>, (?i) for icontains
//   - regex: the pattern as is (needs a reference answer, below)
//   - contains-all / icontains-all: answer_match contains_all
//   - llm-rubric, model-graded-closedqa, factuality: the judge rubric,
//     imported only into a judge suite (-set grader=judge)
//
// A test with any other assertion type (javascript, python, is-json,
// cost, latency, similar, ...) or with more than one checking assertion is
// skipped with the reason. The reference answer admission needs is the
// test's vars.reference (or vars.expected, vars.answer), else the value of
// an equals, starts-with or contains assertion; a test without one is
// skipped, since a check nothing is shown to pass cannot be admitted.
package promptfoo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// Name is the importer's format name.
const Name = "promptfoo"

// Settings (-set key=value).
const (
	SetGrader        = "grader"
	SetJudgeProvider = "judge_provider"
	SetJudgeModel    = "judge_model"
	SetImage         = "image"
	SetLicense       = "license"
)

const defaultImage = "bench/coding-core-base:1"

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func init() { importers.Register(&importer{}) }

type importer struct{}

// Info implements importers.Importer.
func (importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"promptfooconfig.yaml (prompts, tests, assert)"},
		Source:     "your promptfoo project folder or its promptfooconfig.yaml",
		License:    "yours (-set license=...; default proprietary)",
		Track:      schemas.TrackQA,
		Grading:    schemas.GradingVerified,
		Needs:      []string{"a judge model pinned with -set grader=judge for llm-rubric tests"},
		Summary:    "promptfoo test cases to answer tasks: text assertions become checks, llm-rubric becomes the judge rubric",
	}
}

// config is the part of promptfooconfig.yaml the importer reads.
type config struct {
	Description string `yaml:"description"`
	Prompts     any    `yaml:"prompts"`
	Tests       any    `yaml:"tests"`
	DefaultTest *test  `yaml:"defaultTest"`
}

type test struct {
	Description string         `yaml:"description"`
	Vars        map[string]any `yaml:"vars"`
	Assert      []assertion    `yaml:"assert"`
}

type assertion struct {
	Type  string `yaml:"type"`
	Value any    `yaml:"value"`
}

// Import implements importers.Importer.
func (importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if !idRE.MatchString(opts.SuiteID) {
		return nil, fmt.Errorf("promptfoo: suite id %q: want lower-case letters, digits, '.', '_' or '-'", opts.SuiteID)
	}
	for k := range opts.Extra {
		switch k {
		case SetGrader, SetJudgeProvider, SetJudgeModel, SetImage, SetLicense:
		default:
			return nil, fmt.Errorf("promptfoo: unknown setting %q (known: grader, judge_provider, judge_model, image, license)", k)
		}
	}
	grader := scaffold.RowGraderExact
	if g := opts.Extra[SetGrader]; g != "" {
		if g != scaffold.RowGraderExact && g != scaffold.RowGraderJudge {
			return nil, fmt.Errorf("promptfoo: -set grader=%s: want answer_match or judge", g)
		}
		grader = g
	}
	path, err := findConfig(opts.From)
	if err != nil {
		return nil, err
	}
	base := filepath.Dir(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("promptfoo: %w", err)
	}
	var cfg config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("promptfoo: %s: %w", filepath.Base(path), err)
	}
	prompts, err := readPrompts(cfg.Prompts, base)
	if err != nil {
		return nil, err
	}
	tests, err := readTests(cfg.Tests, base)
	if err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir(), Tasks: []string{}, Skipped: []importers.Skip{}}
	var rows []scaffold.Row
	n := 0
	for ti, t := range tests {
		t = merge(cfg.DefaultTest, t)
		for pi, p := range prompts {
			n++
			id := fmt.Sprintf("test-%d", ti+1)
			if len(prompts) > 1 {
				id = fmt.Sprintf("test-%d-prompt-%d", ti+1, pi+1)
			}
			r, reason := rowFor(t, p, grader)
			if reason != "" {
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: reason})
				continue
			}
			r.Line, r.ID = n, id
			r.Title = t.Description
			if r.Title == "" {
				r.Title = id
			}
			rows = append(rows, r)
		}
	}
	if len(opts.IDs) > 0 {
		want := map[string]bool{}
		for _, id := range opts.IDs {
			want[strings.TrimSpace(id)] = true
		}
		kept := rows[:0]
		for _, r := range rows {
			if want[r.ID] {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if opts.Limit > 0 && len(rows) > opts.Limit {
		rows = rows[:opts.Limit]
	}
	suiteDir := opts.SuiteDir()
	raw, err := ensureSuite(suiteDir, opts, grader)
	if err != nil {
		return nil, err
	}
	license := opts.Extra[SetLicense]
	if license == "" {
		license = "proprietary"
	}
	tasks, problems := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: grader, Source: "promptfoo " + filepath.Base(path), License: license,
		Taken: func(id string) bool {
			_, err := os.Lstat(filepath.Join(suiteDir, "tasks", id))
			return err == nil && !opts.Force
		}})
	for _, p := range problems {
		res.Skipped = append(res.Skipped, importers.Skip{ID: fmt.Sprintf("case %d", p.Line), Reason: p.Message})
	}
	edited := raw
	for _, t := range tasks {
		if err := scaffold.WriteFiles(filepath.Join(suiteDir, "tasks", t.ID), t.Files, opts.Force); err != nil {
			return nil, fmt.Errorf("promptfoo: write %s: %w", t.ID, err)
		}
		if edited, _, err = scaffold.AddTaskID(edited, t.ID); err != nil {
			return nil, err
		}
		res.Tasks = append(res.Tasks, t.ID)
	}
	if len(res.Tasks) == 0 {
		return res, errors.New("promptfoo: no task was imported; see the skipped list")
	}
	if err := os.WriteFile(filepath.Join(suiteDir, suite.SuiteFile), edited, 0o644); err != nil {
		return nil, fmt.Errorf("promptfoo: %w", err)
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes, "bifrost-bench suite lint "+opts.SuiteID, "bifrost-bench suite lock "+opts.SuiteID, "bifrost-bench suite admit "+opts.SuiteID)
	return res, nil
}

// findConfig accepts the config file or a folder holding promptfooconfig.yaml (or .yml).
func findConfig(from string) (string, error) {
	if fi, err := os.Stat(from); err == nil && !fi.IsDir() {
		return from, nil
	}
	for _, n := range []string{"promptfooconfig.yaml", "promptfooconfig.yml"} {
		p := filepath.Join(from, n)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("promptfoo: no promptfooconfig.yaml in %s", from)
}

// readFileRef reads "file://x" relative to base; other strings are returned as is.
func readFileRef(v, base string) (string, bool, error) {
	if !strings.HasPrefix(v, "file://") {
		return v, false, nil
	}
	rel := strings.TrimPrefix(v, "file://")
	p := filepath.Clean(filepath.Join(base, filepath.FromSlash(rel)))
	if r, err := filepath.Rel(base, p); err != nil || strings.HasPrefix(r, "..") {
		return "", true, fmt.Errorf("promptfoo: %s is outside the config folder", v)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", true, fmt.Errorf("promptfoo: %w", err)
	}
	return string(b), true, nil
}

// readPrompts reads prompts: a string, a list of strings (inline or
// file://), or a list of {raw|id, label}.
func readPrompts(v any, base string) ([]string, error) {
	var items []any
	switch x := v.(type) {
	case nil:
		return nil, errors.New("promptfoo: the config has no prompts")
	case string:
		items = []any{x}
	case []any:
		items = x
	default:
		return nil, errors.New("promptfoo: prompts: want a string or a list")
	}
	var out []string
	for _, it := range items {
		var s string
		switch x := it.(type) {
		case string:
			s = x
		case map[string]any:
			if r, ok := x["raw"].(string); ok {
				s = r
			} else if id, ok := x["id"].(string); ok {
				s = id
			}
		}
		if s == "" {
			return nil, fmt.Errorf("promptfoo: prompt %v: want text, file:// or {raw: ...}", it)
		}
		text, _, err := readFileRef(s, base)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(strings.TrimSpace(text), "[") {
			return nil, errors.New("promptfoo: chat-format (JSON message list) prompts are not supported yet; give a text prompt")
		}
		out = append(out, text)
	}
	return out, nil
}

// readTests reads tests: a list, or file:// to a YAML list.
func readTests(v any, base string) ([]test, error) {
	if s, ok := v.(string); ok {
		text, isFile, err := readFileRef(s, base)
		if err != nil {
			return nil, err
		}
		if !isFile {
			return nil, errors.New("promptfoo: tests: want a list or file://tests.yaml")
		}
		var ts []test
		if err := yaml.Unmarshal([]byte(text), &ts); err != nil {
			return nil, fmt.Errorf("promptfoo: %s: %w (CSV test files are not supported; use task import-rows)", s, err)
		}
		return ts, nil
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var ts []test
	if err := yaml.Unmarshal(b, &ts); err != nil {
		return nil, fmt.Errorf("promptfoo: tests: %w", err)
	}
	if len(ts) == 0 {
		return nil, errors.New("promptfoo: the config has no tests")
	}
	return ts, nil
}

// merge applies defaultTest: its vars under the test's, its asserts first.
func merge(def *test, t test) test {
	if def == nil {
		return t
	}
	out := test{Description: t.Description, Vars: map[string]any{}}
	for k, v := range def.Vars {
		out.Vars[k] = v
	}
	for k, v := range t.Vars {
		out.Vars[k] = v
	}
	out.Assert = append(append([]assertion{}, def.Assert...), t.Assert...)
	return out
}

var varRE = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// render fills {{var}} placeholders; an unknown var is an error.
func render(prompt string, vars map[string]any) (string, error) {
	var missing []string
	out := varRE.ReplaceAllStringFunc(prompt, func(m string) string {
		k := varRE.FindStringSubmatch(m)[1]
		v, ok := vars[k]
		if !ok {
			missing = append(missing, k)
			return m
		}
		return fmt.Sprint(v)
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("prompt uses %s, which the test does not set", strings.Join(missing, ", "))
	}
	if strings.Contains(out, "{%") {
		return "", errors.New("prompt uses Nunjucks tags ({% ... %}), which are not supported")
	}
	return strings.TrimSpace(out), nil
}

var judgeTypes = map[string]bool{"llm-rubric": true, "model-graded-closedqa": true, "factuality": true}

// rowFor maps one test and prompt to a row, or says why not.
func rowFor(t test, prompt, grader string) (scaffold.Row, string) {
	q, err := render(prompt, t.Vars)
	if err != nil {
		return scaffold.Row{}, err.Error()
	}
	r := scaffold.Row{Question: q}
	for _, k := range []string{"reference", "expected", "answer"} {
		if v, ok := t.Vars[k]; ok {
			r.Reference = fmt.Sprint(v)
			break
		}
	}
	var checks []assertion
	for _, a := range t.Assert {
		if strings.HasPrefix(a.Type, "not-") {
			return r, fmt.Sprintf("assertion %s: negated assertions have no Heimdall check", a.Type)
		}
		checks = append(checks, a)
	}
	if len(checks) != 1 {
		return r, fmt.Sprintf("%d checking assertions: Heimdall imports tests with exactly one", len(checks))
	}
	a := checks[0]
	val, _ := a.Value.(string)
	switch {
	case judgeTypes[a.Type]:
		if grader != scaffold.RowGraderJudge {
			return r, "assertion " + a.Type + ": import it into a judge suite (-set grader=judge -set judge_provider=... -set judge_model=...)"
		}
		if val == "" {
			if v, ok := t.Vars["expected"]; ok {
				val = fmt.Sprint(v)
			}
		}
		r.Rubric = val
		if a.Type == "factuality" {
			r.Rubric = "The answer must be factually consistent with: " + val
		}
	case a.Type == "equals" && val != "":
		r.Match, r.Expected = "regex", []string{`^\s*` + regexp.QuoteMeta(val) + `\s*$`}
		setRef(&r, val)
	case a.Type == "starts-with" && val != "":
		r.Match, r.Expected = "regex", []string{`^\s*` + regexp.QuoteMeta(val)}
		setRef(&r, val)
	case (a.Type == "contains" || a.Type == "icontains") && val != "":
		re := regexp.QuoteMeta(val)
		if a.Type == "icontains" {
			re = "(?i)" + re
		}
		r.Match, r.Expected = "regex", []string{re}
		setRef(&r, val)
	case a.Type == "regex" && val != "":
		if _, err := regexp.Compile(val); err != nil {
			return r, "regex " + val + ": " + err.Error()
		}
		r.Match, r.Expected = "regex", []string{val}
	case a.Type == "contains-all" || a.Type == "icontains-all":
		var vals []string
		if l, ok := a.Value.([]any); ok {
			for _, v := range l {
				vals = append(vals, fmt.Sprint(v))
			}
		}
		if len(vals) == 0 {
			return r, a.Type + " without a list of values"
		}
		r.Match, r.Expected = "contains_all", vals
		setRef(&r, strings.Join(vals, " "))
	default:
		return r, "assertion " + a.Type + " has no Heimdall check (supported: equals, starts-with, contains, icontains, regex, contains-all, icontains-all, llm-rubric, model-graded-closedqa, factuality)"
	}
	if r.Reference == "" {
		return r, "no reference answer: set vars.reference so admission can show the check passes a correct answer"
	}
	return r, ""
}

func setRef(r *scaffold.Row, v string) {
	if r.Reference == "" {
		r.Reference = v
	}
}

// ensureSuite creates the question-and-answer suite when absent.
func ensureSuite(dir string, opts importers.Options, grader string) ([]byte, error) {
	path := filepath.Join(dir, suite.SuiteFile)
	if raw, err := os.ReadFile(path); err == nil {
		var s struct {
			Grader string `yaml:"grader"`
			Output struct {
				Kind string `yaml:"kind"`
			} `yaml:"output"`
		}
		if err := yaml.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
		if s.Output.Kind != "answer" || s.Grader != grader {
			return nil, fmt.Errorf("promptfoo: suite %s exists and is not a question-and-answer suite graded by %s", opts.SuiteID, grader)
		}
		return raw, nil
	}
	image := opts.Extra[SetImage]
	if image == "" {
		image = defaultImage
	}
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: opts.SuiteID, Template: scaffold.TemplateQA, Image: image})
	if err != nil {
		return nil, err
	}
	if files, err = scaffold.BlankSuite(files); err != nil {
		return nil, err
	}
	for i, f := range files {
		if f.Path == suite.SuiteFile && grader == scaffold.RowGraderJudge {
			j := &scaffold.JudgeConfig{Provider: opts.Extra[SetJudgeProvider], Model: opts.Extra[SetJudgeModel]}
			if files[i].Data, err = scaffold.SetSuiteGrader(f.Data, grader, j); err != nil {
				return nil, fmt.Errorf("promptfoo: %w (give -set judge_provider=... -set judge_model=...)", err)
			}
		}
	}
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
