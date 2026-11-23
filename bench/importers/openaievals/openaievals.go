// Package openaievals imports evals written for OpenAI Evals
// (github.com/openai/evals, MIT; the hosted OpenAI Evals product shuts down
// on 30 Nov 2026) into a Heimdall question-and-answer suite.
//
// It reads an evals registry folder: evals/*.yaml name each eval, its class
// and its samples file under data/. Each sample becomes one answer task:
//
//   - basic.match:Match (the answer starts with an ideal) becomes a regex
//     answer anchored at the start;
//   - basic.includes:Includes (an ideal appears in the answer) becomes an
//     unanchored regex, case-insensitive when the eval says ignore_case;
//   - basic.fuzzy_match:FuzzyMatch becomes a case-insensitive contains
//     regex (one direction only: the ideal inside the answer);
//   - modelgraded.classify:ModelBasedClassify becomes a judged task whose
//     rubric is the eval's model-graded prompt with the sample filled in,
//     imported only into a judge suite (-set grader=judge);
//   - any other class is skipped with the reason.
//
// Few-shot files are not imported: the agent gets the sample alone.
package openaievals

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
const Name = "openai-evals"

// Settings (-set key=value).
const (
	SetEvals         = "evals"          // comma-separated eval names; default all
	SetGrader        = "grader"         // answer_match (default) or judge
	SetJudgeProvider = "judge_provider" // judge provider to pin (grader=judge)
	SetJudgeModel    = "judge_model"    // judge model to pin (grader=judge)
	SetImage         = "image"          // workspace image; default bench/coding-core-base:1
	SetLicense       = "license"        // licence of the samples; default MIT
	SetSource        = "source"         // provenance source; default the evals repository
)

const (
	defaultImage  = "bench/coding-core-base:1"
	defaultSource = "https://github.com/openai/evals"
)

// Supported eval classes.
const (
	classMatch      = "evals.elsuite.basic.match:Match"
	classIncludes   = "evals.elsuite.basic.includes:Includes"
	classFuzzy      = "evals.elsuite.basic.fuzzy_match:FuzzyMatch"
	classModelGrade = "evals.elsuite.modelgraded.classify:ModelBasedClassify"
)

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

func init() { importers.Register(&importer{}) }

type importer struct{}

// Info implements importers.Importer.
func (importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"openai/evals registry (Match, Includes, FuzzyMatch, ModelBasedClassify evals)", "an exported eval: evals/<name>.yaml plus data/<name>/samples.jsonl"},
		Source:     "git clone https://github.com/openai/evals && cd evals && git lfs pull --include 'evals/registry/data/<name>/*' (then -from evals/evals/registry)",
		License:    "MIT (the evals repository); your own exported evals keep their licence (-set license=...)",
		Track:      schemas.TrackQA,
		Grading:    schemas.GradingVerified,
		Needs:      []string{"a judge model pinned with -set grader=judge for model-graded evals"},
		Summary:    "OpenAI Evals samples to answer tasks: ideal answers become regex checks, model-graded specs become the judge rubric",
	}
}

// evalSpec is one resolved eval: its id (name.split.version) and arguments.
type evalSpec struct {
	Name    string // the short name (top-level key with id:)
	ID      string // name.dev.v0
	Class   string
	Samples string // samples_jsonl relative to data/
	Args    map[string]any
}

// Import implements importers.Importer.
func (importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if !idRE.MatchString(opts.SuiteID) {
		return nil, fmt.Errorf("openai-evals: suite id %q: want lower-case letters, digits, '.', '_' or '-'", opts.SuiteID)
	}
	regDir, err := findRegistry(opts.From)
	if err != nil {
		return nil, err
	}
	grader := scaffold.RowGraderExact
	if g := opts.Extra[SetGrader]; g != "" {
		if g != scaffold.RowGraderExact && g != scaffold.RowGraderJudge {
			return nil, fmt.Errorf("openai-evals: -set grader=%s: want answer_match or judge", g)
		}
		grader = g
	}
	for k := range opts.Extra {
		switch k {
		case SetEvals, SetGrader, SetJudgeProvider, SetJudgeModel, SetImage, SetLicense, SetSource:
		default:
			return nil, fmt.Errorf("openai-evals: unknown setting %q (known: evals, grader, judge_provider, judge_model, image, license, source)", k)
		}
	}
	specs, err := readSpecs(filepath.Join(regDir, "evals"))
	if err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir(), Tasks: []string{}, Skipped: []importers.Skip{}}
	if want := opts.Extra[SetEvals]; want != "" {
		byName := map[string]evalSpec{}
		for _, s := range specs {
			byName[s.Name] = s
			byName[s.ID] = s
		}
		var picked []evalSpec
		for _, n := range strings.Split(want, ",") {
			n = strings.TrimSpace(n)
			if s, ok := byName[n]; ok {
				picked = append(picked, s)
			} else {
				res.Skipped = append(res.Skipped, importers.Skip{ID: n, Reason: "no eval with this name under " + filepath.Join(regDir, "evals")})
			}
		}
		specs = picked
	}
	suiteDir := opts.SuiteDir()
	suitePath := filepath.Join(suiteDir, suite.SuiteFile)
	raw, err := ensureSuite(suiteDir, opts, grader)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	var existing struct {
		Tasks []string `yaml:"tasks"`
	}
	_ = yaml.Unmarshal(raw, &existing)
	for _, id := range existing.Tasks {
		taken[id] = true
	}
	license := opts.Extra[SetLicense]
	if license == "" {
		license = "MIT"
	}
	source := opts.Extra[SetSource]
	if source == "" {
		source = defaultSource
	}
	ids := map[string]bool{}
	for _, id := range opts.IDs {
		ids[strings.TrimSpace(id)] = true
	}
	edited := raw
	for _, s := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opts.Limit > 0 && len(res.Tasks) >= opts.Limit {
			break
		}
		rows, reason := rowsFor(s, regDir, grader)
		if reason != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: s.Name, Reason: reason})
			continue
		}
		if len(ids) > 0 {
			kept := rows[:0]
			for _, r := range rows {
				if ids[r.ID] {
					kept = append(kept, r)
				}
			}
			rows = kept
		}
		if opts.Limit > 0 && len(res.Tasks)+len(rows) > opts.Limit {
			rows = rows[:opts.Limit-len(res.Tasks)]
		}
		tasks, problems := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: grader, Source: source + " (" + s.ID + ")", License: license,
			Taken: func(id string) bool {
				if taken[id] {
					return true
				}
				_, err := os.Lstat(filepath.Join(suiteDir, "tasks", id))
				return err == nil && !opts.Force
			}})
		for _, p := range problems {
			res.Skipped = append(res.Skipped, importers.Skip{ID: fmt.Sprintf("%s#%d", s.Name, p.Line), Reason: p.Message})
		}
		for _, t := range tasks {
			if err := scaffold.WriteFiles(filepath.Join(suiteDir, "tasks", t.ID), t.Files, opts.Force); err != nil {
				return nil, fmt.Errorf("openai-evals: write %s: %w", t.ID, err)
			}
			if edited, _, err = scaffold.AddTaskID(edited, t.ID); err != nil {
				return nil, err
			}
			taken[t.ID] = true
			res.Tasks = append(res.Tasks, t.ID)
		}
	}
	if len(res.Tasks) == 0 {
		return res, importers.NoTaskError("openai-evals", res.Skipped)
	}
	if err := os.WriteFile(suitePath, edited, 0o644); err != nil {
		return nil, fmt.Errorf("openai-evals: %w", err)
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes,
		"bifrost-bench suite lint "+opts.SuiteID,
		"bifrost-bench suite lock "+opts.SuiteID,
		"bifrost-bench suite admit "+opts.SuiteID)
	if opts.Logger != nil {
		opts.Logger.Info("openai-evals import", "suite", opts.SuiteID, "tasks", len(res.Tasks), "skipped", len(res.Skipped))
	}
	return res, nil
}

// findRegistry accepts the evals checkout, its evals/ package or the
// registry folder itself (the one holding evals/ and data/).
func findRegistry(from string) (string, error) {
	for _, c := range []string{from, filepath.Join(from, "registry"), filepath.Join(from, "evals", "registry")} {
		if fi, err := os.Stat(filepath.Join(c, "evals")); err == nil && fi.IsDir() {
			if fi, err := os.Stat(filepath.Join(c, "data")); err == nil && fi.IsDir() {
				return c, nil
			}
		}
	}
	return "", fmt.Errorf("openai-evals: -from %s holds no registry (a folder with evals/ and data/)", from)
}

// readSpecs reads every evals/*.yaml and resolves each short name through
// its id to the class and arguments.
func readSpecs(dir string) ([]evalSpec, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []evalSpec
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc map[string]map[string]any
		if err := yaml.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("openai-evals: %s: %w", filepath.Base(f), err)
		}
		names := make([]string, 0, len(doc))
		for k := range doc {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			id, _ := doc[name]["id"].(string)
			if id == "" {
				continue
			}
			body, ok := doc[id]
			if !ok {
				continue
			}
			s := evalSpec{Name: name, ID: id}
			s.Class, _ = body["class"].(string)
			s.Args, _ = body["args"].(map[string]any)
			if s.Args != nil {
				s.Samples, _ = s.Args["samples_jsonl"].(string)
			}
			out = append(out, s)
		}
	}
	return out, nil
}

// ensureSuite creates the question-and-answer suite when absent and
// returns suite.yaml. An existing suite must be one with the same grader.
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
			return nil, fmt.Errorf("openai-evals: %s: %w", path, err)
		}
		if s.Output.Kind != "answer" || s.Grader != grader {
			return nil, fmt.Errorf("openai-evals: suite %s exists and is not a question-and-answer suite graded by %s", opts.SuiteID, grader)
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
		if f.Path != suite.SuiteFile {
			continue
		}
		if grader == scaffold.RowGraderJudge {
			j := &scaffold.JudgeConfig{Provider: opts.Extra[SetJudgeProvider], Model: opts.Extra[SetJudgeModel]}
			if files[i].Data, err = scaffold.SetSuiteGrader(f.Data, grader, j); err != nil {
				return nil, fmt.Errorf("openai-evals: %w (give -set judge_provider=... -set judge_model=...)", err)
			}
		}
	}
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		return nil, fmt.Errorf("openai-evals: %w", err)
	}
	return os.ReadFile(path)
}

// rowsFor turns one eval's samples into rows, or says why it is skipped.
func rowsFor(s evalSpec, regDir, grader string) ([]scaffold.Row, string) {
	if s.Samples == "" {
		return nil, "no samples_jsonl argument"
	}
	switch s.Class {
	case classMatch, classIncludes, classFuzzy:
	case classModelGrade:
		if grader != scaffold.RowGraderJudge {
			return nil, "model-graded eval: import it into a judge suite (-set grader=judge -set judge_provider=... -set judge_model=...)"
		}
	default:
		return nil, fmt.Sprintf("class %s has no Heimdall mapping (supported: Match, Includes, FuzzyMatch, ModelBasedClassify)", s.Class)
	}
	path := filepath.Join(regDir, "data", filepath.FromSlash(s.Samples))
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "samples file " + s.Samples + ": " + err.Error()
	}
	if bytes.HasPrefix(b, []byte("version https://git-lfs")) {
		return nil, "samples file " + s.Samples + " is a Git LFS pointer: run git lfs pull for it first"
	}
	var rubric string
	if s.Class == classModelGrade {
		if rubric, err = modelGradedPrompt(regDir, s.Args); err != nil {
			return nil, err.Error()
		}
	}
	ignoreCase, _ := s.Args["ignore_case"].(bool)
	var rows []scaffold.Row
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	line := 0
	for sc.Scan() {
		text := bytes.TrimSpace(sc.Bytes())
		line++
		if len(text) == 0 {
			continue
		}
		var all map[string]json.RawMessage
		if err := json.Unmarshal(text, &all); err != nil {
			rows = append(rows, scaffold.Row{Line: line}) // ImportRows reports it
			continue
		}
		q := renderInput(all["input"])
		ideals := idealList(all["ideal"])
		r := scaffold.Row{Line: line, ID: fmt.Sprintf("%s-%d", slug(s.Name), line), Title: fmt.Sprintf("%s, sample %d", s.Name, line), Question: q, Category: s.Name}
		switch s.Class {
		case classMatch:
			r.Match, r.Expected = "regex", regexes(ideals, `^\s*`, false)
		case classIncludes:
			r.Match, r.Expected = "regex", regexes(ideals, "", ignoreCase)
		case classFuzzy:
			r.Match, r.Expected = "regex", regexes(ideals, "", true)
		case classModelGrade:
			r.Rubric = fillPrompt(rubric, all)
			r.Expected = ideals
		}
		if len(ideals) > 0 {
			r.Reference = ideals[0]
		}
		if q == "" || (len(ideals) == 0 && s.Class != classModelGrade) {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, "no sample with an input and an ideal answer"
	}
	return rows, ""
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// regexes quotes each ideal as a regex with prefix, optionally
// case-insensitive.
func regexes(ideals []string, prefix string, fold bool) []string {
	out := make([]string, 0, len(ideals))
	for _, i := range ideals {
		re := prefix + regexp.QuoteMeta(strings.TrimSpace(i))
		if fold {
			re = "(?i)" + re
		}
		out = append(out, re)
	}
	return out
}

// renderInput turns a sample input (a string or chat messages) into one
// question. A lone user message is its content; otherwise each message is
// written with its role.
func renderInput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var msgs []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
		Name    string `json:"name"`
	}
	if json.Unmarshal(raw, &msgs) != nil {
		return ""
	}
	if len(msgs) == 1 {
		return strings.TrimSpace(contentText(msgs[0].Content))
	}
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if m.Name != "" {
			role += " (" + m.Name + ")"
		}
		if role == "" {
			role = "user"
		}
		fmt.Fprintf(&b, "%s: %s\n\n", role, strings.TrimSpace(contentText(m.Content)))
	}
	return strings.TrimSpace(b.String())
}

// contentText reads a message content: a string or a list of text parts.
func contentText(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, p := range v {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// idealList reads ideal as a string or a list of strings.
func idealList(raw json.RawMessage) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []string{s}
	}
	var l []any
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	var out []string
	for _, v := range l {
		switch x := v.(type) {
		case string:
			if strings.TrimSpace(x) != "" {
				out = append(out, x)
			}
		case float64, bool:
			out = append(out, fmt.Sprint(x))
		}
	}
	return out
}

// modelGradedPrompt reads the eval's modelgraded_spec from
// registry/modelgraded/*.yaml and returns its prompt with its choice
// instructions.
func modelGradedPrompt(regDir string, args map[string]any) (string, error) {
	name, _ := args["modelgraded_spec"].(string)
	if name == "" {
		return "", errors.New("model-graded eval without modelgraded_spec")
	}
	files, _ := filepath.Glob(filepath.Join(regDir, "modelgraded", "*.yaml"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc map[string]struct {
			Prompt        any               `yaml:"prompt"`
			ChoiceStrings any               `yaml:"choice_strings"`
			ChoiceScores  map[string]any    `yaml:"choice_scores"`
			InputOutputs  map[string]string `yaml:"input_outputs"`
		}
		if yaml.Unmarshal(b, &doc) != nil {
			continue
		}
		spec, ok := doc[name]
		if !ok {
			continue
		}
		prompt := ""
		switch p := spec.Prompt.(type) {
		case string:
			prompt = p
		case []any:
			for _, m := range p {
				if mm, ok := m.(map[string]any); ok {
					if c, ok := mm["content"].(string); ok {
						prompt += c + "\n"
					}
				}
			}
		}
		if prompt == "" {
			return "", fmt.Errorf("modelgraded spec %s has no prompt", name)
		}
		var pass []string
		for k, v := range spec.ChoiceScores {
			if f, ok := v.(float64); ok && f >= 1 {
				pass = append(pass, k)
			} else if i, ok := v.(int); ok && i >= 1 {
				pass = append(pass, k)
			}
		}
		sort.Strings(pass)
		rubric := "Grade the answer as the OpenAI Evals model-graded spec \"" + name + "\" does. The grading prompt, with the answer as {completion}:\n\n" + strings.TrimSpace(prompt)
		if len(pass) > 0 {
			rubric += "\n\nThe answer passes when the grading prompt's choice would be: " + strings.Join(pass, " or ") + "."
		}
		return rubric, nil
	}
	return "", fmt.Errorf("modelgraded spec %q not found under %s", name, filepath.Join(regDir, "modelgraded"))
}

var placeholderRE = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)

// fillPrompt replaces {field} with the sample's field, leaving {completion}
// (the agent's answer) for the judge.
func fillPrompt(prompt string, sample map[string]json.RawMessage) string {
	return placeholderRE.ReplaceAllStringFunc(prompt, func(m string) string {
		key := m[1 : len(m)-1]
		if key == "completion" {
			return m
		}
		raw, ok := sample[key]
		if !ok {
			return m
		}
		if key == "input" {
			return renderInput(raw)
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		if l := idealList(raw); len(l) > 0 {
			return strings.Join(l, " | ")
		}
		return string(raw)
	})
}
