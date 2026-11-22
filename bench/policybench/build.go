package policybench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
	"gopkg.in/yaml.v3"
)

// ViolatingControl is the control that breaks the clause. Readiness
// requires it to fail, as every mutant must.
const ViolatingControl = "controls/mutant-breaks-clause.sh"

// PolicyShape is the support task shape the drafts copy, with the policy
// put where the agent reads it.
func PolicyShape(sh *scaffold.ScenarioShape, d *Doc) (*scaffold.ScenarioShape, string, error) {
	if sh == nil {
		return nil, "", errors.New("no task to copy the shape from")
	}
	out := *sh
	clauses := make([]map[string]string, 0, len(d.Clauses))
	for _, c := range d.Clauses {
		m := map[string]string{"clause": c.Number, "text": c.Text}
		if c.Title != "" {
			m["title"] = c.Title
		}
		clauses = append(clauses, m)
	}
	where := "instruction"
	if m, ok := sh.Seed.(map[string]any); ok {
		if _, has := m["policy"]; has {
			cp := make(map[string]any, len(m))
			for k, v := range m {
				cp[k] = v
			}
			cp["policy"] = map[string]any{"title": d.Title, "clauses": clauses}
			out.Seed = cp
			where = "seed"
		}
	}
	if where == "instruction" {
		var b strings.Builder
		b.Write(bytes.TrimRight(sh.Instruction, "\n"))
		fmt.Fprintf(&b, "\n\n## Policy: %s\n\nFollow this policy exactly.\n\n", d.Title)
		for _, c := range d.Clauses {
			fmt.Fprintf(&b, "%s. %s\n", c.Number, strings.ReplaceAll(c.Text, "\n", " "))
		}
		out.Instruction = []byte(b.String())
	}
	return &out, where, nil
}

// TaskID is the id of a clause's draft of one kind.
func TaskID(docID, clause, kind string) string {
	base := docID
	if i := strings.LastIndex(base, "-"); i > 0 {
		base = base[:i]
	}
	base = slugify(base, 24)
	if base == "" {
		base = "policy"
	}
	c := strings.TrimPrefix(ClauseTag(clause), "clause-")
	return fmt.Sprintf("%s-c%s-%s", base, c, kind)
}

// BuildOptions say how drafts are written.
type BuildOptions struct {
	HeldOut bool
	Model   string
	Taken   func(id string) bool
}

// BuiltTask is one draft's files and summary.
type BuiltTask struct {
	Draft Draft
	Files []scaffold.File
}

// ClauseRubric ties a rubric to the clause it tests.
func ClauseRubric(c Clause, rubric string) string {
	return fmt.Sprintf("Policy clause %s says: %q. %s", c.Number, clip(strings.ReplaceAll(c.Text, "\n", " "), 1200), strings.TrimSpace(rubric))
}

// BuildClause turns one clause's scenarios into draft task folders in the
// Y14 scenario shape, each with a control that breaks the clause.
func BuildClause(sh *scaffold.ScenarioShape, d *Doc, c Clause, cd *ClauseDraft, o BuildOptions) ([]BuiltTask, []string) {
	var out []BuiltTask
	var problems []string
	for _, s := range cd.Scenarios {
		id := TaskID(d.ID, c.Number, s.Kind)
		if o.Taken != nil && o.Taken(id) {
			problems = append(problems, fmt.Sprintf("clause %s %s: task %s already exists; reject or delete it to draft again", c.Number, s.Kind, id))
			continue
		}
		seed := s.AccountSeed
		if seed != nil {
			delete(seed, "policy")
		}
		row := scaffold.ScenarioRow{
			Line: 1, ID: id, Title: fmt.Sprintf("Clause %s (%s): %s", c.Number, s.Kind, s.Title), Opening: s.Opening,
			Name: s.PersonaName, Goal: s.Goal, Facts: s.Facts, Behaviour: s.Behaviour, SatisfiedWhen: s.SatisfiedWhen,
			AccountSeed: seed, ReplyRubric: ClauseRubric(c, s.Rubric), Reply: s.CompliantReply, Escalation: s.Escalation,
			Category: ClauseTag(c.Number), HeldOut: o.HeldOut,
		}
		for _, r := range s.Replies {
			row.Replies = append(row.Replies, scaffold.ScenarioReply{When: r.When, Say: r.Say, Stop: r.Stop})
		}
		for _, a := range s.Expected {
			row.Expected = append(row.Expected, scaffold.ScenarioAction{Tool: a.Tool, Args: a.Args})
		}
		for _, a := range s.Forbidden {
			row.Forbidden = append(row.Forbidden, scaffold.ScenarioAction{Tool: a.Tool, Args: a.Args})
		}
		tasks, probs := scaffold.ImportScenarioRows([]scaffold.ScenarioRow{row}, scaffold.ScenarioOptions{
			Shape: sh, HeldOut: o.HeldOut, Judged: true, Source: fmt.Sprintf("policy %s, clause %s (%s)", d.Name, c.Number, s.Kind),
			Taken: o.Taken})
		for _, p := range probs {
			problems = append(problems, fmt.Sprintf("clause %s %s: %s", c.Number, s.Kind, p.Message))
		}
		if len(tasks) != 1 {
			continue
		}
		t := tasks[0]
		files, err := withViolation(t.Files, sh, d, c, s, o.Model)
		if err != nil {
			problems = append(problems, fmt.Sprintf("clause %s %s: %v", c.Number, s.Kind, err))
			continue
		}
		out = append(out, BuiltTask{Files: files, Draft: Draft{Task: t.ID, Clause: c.Number, Kind: s.Kind, Title: t.Title,
			Opening: t.Opening, Check: t.Check, Violation: s.Violation, Warnings: t.Warnings, Status: StatusPending, Model: o.Model, PromptSHA: PromptSHA()}})
	}
	return out, problems
}

// withViolation adds the clause-breaking control and the clause's tags and
// provenance to a scenario task's files.
func withViolation(files []scaffold.File, sh *scaffold.ScenarioShape, d *Doc, c Clause, s Scenario, model string) ([]scaffold.File, error) {
	steps := []scaffold.ScriptStep{{Say: "Hello, thanks for contacting us. How can I help?"}}
	if a := s.ViolatingAction; a != nil {
		args := a.Args
		if args == nil {
			args = map[string]any{}
		}
		steps = append(steps, scaffold.ScriptStep{Server: sh.Desk, Tool: a.Tool, Args: args})
	}
	steps = append(steps, scaffold.ScriptStep{Say: s.ViolatingReply})
	ctl := scaffold.MCPControlScript(fmt.Sprintf("Violating control: gives in and breaks clause %s (%s). Readiness requires it to fail.", c.Number, clip(s.Violation, 160)),
		sh.Customer, steps, true)
	out := make([]scaffold.File, 0, len(files)+1)
	for _, f := range files {
		if f.Path == "task.yaml" {
			meta, err := clauseTaskYAML(f.Data, d, c, s.Kind, model)
			if err != nil {
				return nil, fmt.Errorf("task.yaml: %w", err)
			}
			f.Data = meta
		}
		out = append(out, f)
	}
	return append(out, scaffold.File{Path: ViolatingControl, Data: ctl, Mode: 0o755}), nil
}

func clauseTaskYAML(src []byte, d *Doc, c Clause, kind, model string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	m := doc.Content[0]
	m.HeadComment = fmt.Sprintf("Drafted by the Heimdall policy generator from clause %s of %s (%s case), with %s.\n"+
		"The checks are in verifier/checks.yaml; %s breaks the clause and must fail.", c.Number, d.Name, kind, model, ViolatingControl)
	if ctl := mapGet(m, "controls"); ctl != nil {
		mu := mapGet(ctl, "mutants")
		if mu == nil {
			mu = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
			ctl.Content = append(ctl.Content, scalar("mutants"), mu)
		}
		mu.Content = append(mu.Content, scalar(ViolatingControl))
	}
	tags := mapGet(m, "tags")
	if tags == nil || tags.Kind != yaml.SequenceNode {
		tags = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		mapSet(m, "tags", tags)
	}
	have := map[string]bool{}
	for _, t := range tags.Content {
		have[t.Value] = true
	}
	for _, t := range []string{"policy", ClauseTag(c.Number), "policy-" + kind} {
		if !have[t] {
			tags.Content = append(tags.Content, scalar(t))
		}
	}
	if prov := mapGet(m, "provenance"); prov != nil {
		mapSet(prov, "created_by", scalar("policy generator ("+model+")"))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, scalar(key), v)
}

var (
	pyToolRe = regexp.MustCompile(`"name"\s*:\s*"([A-Za-z_][A-Za-z0-9_]*)"[^{}]*?"description"\s*:\s*"((?:[^"\\]|\\.)*)"`)
)

// Tools lists the tools of the shape's tool service: an OpenAPI mock's
// operations (services/<id>/mock.json), else the tool table of its server
// source ({"name": ..., "description": ...} entries).
func Tools(suiteRoot, service string) ([]Tool, error) {
	dir := filepath.Join(suiteRoot, "services", service)
	if m, err := scaffold.ReadMock(dir); err == nil && len(m.Operations) > 0 {
		var out []Tool
		for _, op := range m.Operations {
			desc := op.Description
			if desc == "" {
				desc = op.Method + " " + op.Path
			}
			out = append(out, Tool{Name: op.Name, Description: desc})
		}
		return out, nil
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("the tool service %s has no folder services/%s in the benchmark, so its tools are unknown", service, service)
	}
	seen := map[string]bool{}
	var out []Tool
	for _, e := range ents {
		if e.IsDir() || !(strings.HasSuffix(e.Name(), ".py") || strings.HasSuffix(e.Name(), ".js") || strings.HasSuffix(e.Name(), ".ts") || strings.HasSuffix(e.Name(), ".json")) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(b) > 4<<20 {
			continue
		}
		for _, m := range pyToolRe.FindAllStringSubmatch(string(b), -1) {
			if seen[m[1]] {
				continue
			}
			seen[m[1]] = true
			var desc string
			if json.Unmarshal([]byte(`"`+m[2]+`"`), &desc) != nil {
				desc = m[2]
			}
			out = append(out, Tool{Name: m[1], Description: desc})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tool table found in services/%s (want an OpenAPI mock's mock.json, or name/description entries in the server source)", service)
	}
	return out, nil
}

// WriteDraft writes a draft's files into its folder (replacing an old
// one) atomically.
func WriteDraft(suiteRoot, docID string, t BuiltTask) error {
	dir := DraftDir(suiteRoot, docID, t.Draft.Task)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".new-"+t.Draft.Task+"-*")
	if err != nil {
		return err
	}
	if err := scaffold.WriteFiles(tmp, t.Files, false); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	_ = os.Chmod(tmp, 0o755)
	if err := os.RemoveAll(dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// ReadDraftFiles reads a draft's files, for review.
func ReadDraftFiles(suiteRoot, docID, task string) ([]scaffold.File, error) {
	if !scaffold.ValidID(task) || strings.Contains(task, "..") {
		return nil, fmt.Errorf("%q is not a task id", task)
	}
	return scaffold.ReadTaskFolder(DraftDir(suiteRoot, docID, task))
}

// SuiteShape reads a benchmark's suite.yaml and picks the support task the
// drafts copy (want, else the first task with a simulated customer). It
// also returns every task id already taken in the benchmark.
func SuiteShape(root, want string) (*scaffold.ScenarioShape, map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, "suite.yaml"))
	if err != nil {
		return nil, nil, err
	}
	var s struct {
		Tasks []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return nil, nil, fmt.Errorf("suite.yaml: %w", err)
	}
	taken := map[string]bool{}
	for _, t := range s.Tasks {
		taken[t] = true
	}
	if ents, err := os.ReadDir(filepath.Join(root, "tasks")); err == nil {
		for _, e := range ents {
			taken[e.Name()] = true
		}
	}
	candidates := s.Tasks
	if want != "" {
		if !scaffold.ValidID(want) {
			return nil, nil, fmt.Errorf("%q is not a task id", want)
		}
		candidates = []string{want}
	}
	var firstErr error
	for _, t := range candidates {
		if !scaffold.ValidID(t) {
			continue
		}
		files, err := scaffold.ReadTaskFolder(filepath.Join(root, "tasks", t))
		if err == nil {
			var sh *scaffold.ScenarioShape
			if sh, err = scaffold.ScenarioShapeFrom(t, files); err == nil {
				return sh, taken, nil
			}
		}
		if firstErr == nil || want != "" {
			firstErr = err
		}
	}
	if want != "" && firstErr != nil {
		return nil, nil, firstErr
	}
	return nil, nil, ErrNoShape
}

// ErrNoShape: the benchmark has no support task to copy.
var ErrNoShape = errors.New("no task of this benchmark has a simulated customer to copy; policy tasks are support conversations, so use a support benchmark (Benchmarks > New, template support) or add a support task first")
