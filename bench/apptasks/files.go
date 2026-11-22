package apptasks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/simuser"
)

// CreatedBy is the provenance created_by of every generated task.
const CreatedBy = "api policy generator"

// appDockerfile is the app service image: the mock server and the user
// simulator, so one image serves the app and the simulated customer.
const appDockerfile = `# Image of the app copy Heimdall generated from your OpenAPI file (mock.json
# beside this file) plus the user simulator (simuser.py and prompts/), which
# plays the customer. Python standard library only, so the build needs no
# network beyond the base image. The readiness check builds it when missing.
# The base is pinned by digest (tag kept for reading), as the suite base is.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN useradd -m -u 1000 svc \
 && mkdir -p /seed /state /sim \
 && chown svc:svc /state
COPY server.py /srv/server.py
COPY mock.json /srv/mock.json
COPY simuser.py /srv/simuser.py
COPY prompts/ /srv/prompts/
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 PORT=8000
USER 1000
EXPOSE 8000
CMD ["python3", "-I", "/srv/server.py"]
`

// AppServiceFiles are services/<app>/ of the benchmark: the mock (server,
// mock.json, the OpenAPI file), the simulator and the image.
func AppServiceFiles(m *scaffold.MockSpec, openapi []byte, openapiName string) ([]scaffold.File, error) {
	files, err := scaffold.MockServiceFiles(m, openapi, openapiName)
	if err != nil {
		return nil, err
	}
	out := files[:0]
	for _, f := range files {
		if f.Path == "Dockerfile" {
			f.Data = []byte(appDockerfile)
		}
		out = append(out, f)
	}
	sim := simuser.Files()
	for _, rel := range sortedKeys(sim) {
		out = append(out, scaffold.File{Path: rel, Data: sim[rel], Mode: 0o644})
	}
	return out, nil
}

const shapeTaskYAML = `id: shape
version: 1.0.0
title: shape
project: %[2]s
kind: feature
instruction: instruction.md
repo_dir: repo
services:
  - id: %[2]s
    role: mcp
    # Built from services/%[2]s/ (the app copy from your OpenAPI file); the readiness check builds it when missing.
    image: bench/%[1]s-%[2]s:1
    port: 8000
    mcp: {path: /mcp}
    files:
      /seed/state.json: seed/%[2]s.json
    ready:
      argv: ["python3", "-I", "/srv/server.py", "ready"]
      timeout_s: 30
    export:
      - {id: state.json, path: /state/state.json}
      - {id: calls.jsonl, path: /state/calls.jsonl}
  # The customer: the user simulator (simuser.py, same image) over MCP.
  # gateway: {} marks it as the simulated user.
  - id: customer
    role: mcp
    image: bench/%[1]s-%[2]s:1
    argv: ["python3", "-I", "/srv/simuser.py", "serve", "--config", "/sim/user.json", "--state", "/state"]
    port: 8000
    mcp: {path: /mcp}
    gateway: {}
    files:
      /sim/user.json: user.json
    ready:
      argv: ["python3", "-I", "/srv/simuser.py", "ready"]
      timeout_s: 30
    export:
      - {id: transcript.json, path: /state/transcript.json}
verifier:
  tests: verifier
  required: []
protected: []
timeouts:
  agent_s: 600
capabilities: [tool_use, policy_following, honesty]
difficulty: medium
provenance:
  source: shape
  license: proprietary
  created_by: shape
`

// Instruction is what the agent reads: the desk, the tools and the
// approved policy.
func Instruction(title, app string, rules []Rule) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Support chat\n\nYou're on the %s support desk today. A customer is waiting in the chat.\n", orDefault(title, app))
	b.WriteString("Talk to them only with the `respond_to_user` tool of the customer server (start by\n")
	fmt.Fprintf(&b, "greeting them), use the `%s` tools to look things up and act, and follow the\n", app)
	b.WriteString("policy below exactly. When the customer is done, end your session.\n\n## Policy\n\n")
	b.WriteString(PolicyText(rules))
	return []byte(b.String())
}

// Shape is the task every generated task copies.
func Shape(suiteID, app, title string, rules []Rule, seed State) (*scaffold.ScenarioShape, error) {
	ty := fmt.Sprintf(shapeTaskYAML, suiteID, app)
	seedJSON, err := json.MarshalIndent(stateAny(seed), "", "  ")
	if err != nil {
		return nil, err
	}
	files := []scaffold.File{
		{Path: "task.yaml", Data: []byte(ty), Mode: 0o644},
		{Path: "instruction.md", Data: Instruction(title, app, rules), Mode: 0o644},
		{Path: "seed/" + app + ".json", Data: seedJSON, Mode: 0o644},
		{Path: "user.json", Data: []byte(`{"id": "shape"}`), Mode: 0o644},
		{Path: "repo/README.md", Data: []byte("# Workspace\n\nScratch space. Today's work is done through the " + app + " tools and the customer chat.\n"), Mode: 0o644},
	}
	return scaffold.ScenarioShapeFrom("shape", files)
}

// Row is a plan as a Y14 scenario row.
func Row(line int, p *Plan, app string) scaffold.ScenarioRow {
	sc := p.Scenario
	r := scaffold.ScenarioRow{Line: line, Title: sc.Title, Opening: sc.Opening, Name: sc.Name, Goal: sc.Goal,
		Facts: sc.Facts, Unknown: sc.Unknown, Behaviour: sc.Behaviour, SatisfiedWhen: sc.SatisfiedWhen,
		Ordered: sc.Ordered, ReplyRubric: sc.ReplyRubric, Reply: sc.Reply, Escalation: sc.Escalation,
		Category: "policy " + sc.RuleID}
	for _, rp := range sc.Replies {
		r.Replies = append(r.Replies, scaffold.ScenarioReply{When: rp.When, Say: rp.Say, Stop: rp.Stop})
	}
	if len(sc.AccountSeed) > 0 {
		r.AccountSeed = stateAny(State(sc.AccountSeed))
	}
	for _, a := range sc.Expected {
		r.Expected = append(r.Expected, scaffold.ScenarioAction{Tool: a.Tool, Server: app, Args: a.Args})
	}
	for _, a := range sc.Forbidden {
		args := a.Args
		if args == nil {
			args = map[string]any{}
		}
		r.Forbidden = append(r.Forbidden, scaffold.ScenarioAction{Tool: a.Tool, Server: app, Args: args})
	}
	return r
}

// WriteOptions say how generated tasks are written.
type WriteOptions struct {
	Suite, App, Title string
	Rules             []Rule
	Seed              State
	Judged            bool
	HeldOut           bool
	Source            string // the draft, for provenance
	Taken             func(id string) bool
}

// TaskFiles turns plans into task folders (files relative to tasks/<id>/)
// with the plan's checks.yaml, marked pending review.
func TaskFiles(plans []*Plan, o WriteOptions) ([]scaffold.ScenarioTask, []scaffold.RowProblem, error) {
	sh, err := Shape(o.Suite, o.App, o.Title, o.Rules, o.Seed)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]scaffold.ScenarioRow, len(plans))
	for i, p := range plans {
		rows[i] = Row(i+1, p, o.App)
	}
	tasks, problems := scaffold.ImportScenarioRows(rows, scaffold.ScenarioOptions{Shape: sh, HeldOut: o.HeldOut, Judged: o.Judged,
		Source: o.Source, License: "proprietary", Taken: o.Taken})
	for ti := range tasks {
		t := &tasks[ti]
		p := plans[t.Line-1]
		t.Checks = p.NChecks
		t.Check = strings.Join(p.Summary, "; ")
		for fi := range t.Files {
			f := &t.Files[fi]
			switch f.Path {
			case "verifier/checks.yaml":
				f.Data = p.Checks
			case "task.yaml":
				out, err := markPending(f.Data, o.Source, p.Scenario.RuleID)
				if err != nil {
					return nil, nil, fmt.Errorf("task %s: %w", t.ID, err)
				}
				f.Data = out
			}
		}
	}
	return tasks, problems, nil
}

// markPending rewrites provenance: the generator made it and no person has
// reviewed it yet.
func markPending(src []byte, source, rule string) ([]byte, error) {
	return editYAML(src, func(m *yaml.Node) {
		prov := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setScalar(prov, "source", fmt.Sprintf("%s, policy rule %s", source, rule))
		setScalar(prov, "license", "proprietary")
		setScalar(prov, "created_by", CreatedBy)
		setNode(prov, "instruction_reviewed", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
		setNode(m, "provenance", prov)
		m.HeadComment = strings.Replace(m.HeadComment, "Written by the Heimdall scenario import from one row", "Drafted by the Heimdall API and policy generator", 1)
	})
}

// SuiteFiles is a new benchmark for the app: the support template's base
// image and model outage probe, the app service, grader checks, and the
// tasks list.
func SuiteFiles(o WriteOptions, service []scaffold.File, taskIDs []string, judge *scaffold.JudgeConfig) ([]scaffold.File, error) {
	all, err := scaffold.Suite(scaffold.SuiteOptions{ID: o.Suite, Template: scaffold.TemplateSupport})
	if err != nil {
		return nil, err
	}
	var out []scaffold.File
	var suiteYAML []byte
	for _, f := range all {
		switch {
		case f.Path == "suite.yaml":
			suiteYAML = f.Data
		case f.Path == "probes/model-outage.yaml", strings.HasPrefix(f.Path, "base/"):
			out = append(out, f)
		}
	}
	if suiteYAML == nil {
		return nil, errors.New("the support template has no suite.yaml")
	}
	y, err := SetSuiteTasks(suiteYAML, taskIDs)
	if err != nil {
		return nil, err
	}
	if y, err = editYAML(y, func(m *yaml.Node) {
		setScalar(m, "description", "Support conversations against a copy of "+orDefault(o.Title, o.App)+" built from its OpenAPI file, graded on the app's final state and its policy.")
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "model-outage"})
		setNode(m, "probes", seq)
		m.HeadComment = "Generated by the Heimdall API and policy generator from an OpenAPI file and a policy document.\n" +
			"services/" + o.App + "/ is the app copy (the API's routes over seeded state, as MCP tools too) and the simulated customer.\n" +
			"Each task's verifier/checks.yaml grades the app's exported state, the calls made and, with a judge, the reply."
	}); err != nil {
		return nil, err
	}
	if y, err = scaffold.SetChecksGrader(y, judge); err != nil {
		return nil, err
	}
	out = append(out, scaffold.File{Path: "suite.yaml", Data: y, Mode: 0o644})
	out = append(out, scaffold.File{Path: "README.md", Data: []byte(readme(o)), Mode: 0o644})
	for _, f := range service {
		f.Path = "services/" + o.App + "/" + f.Path
		out = append(out, f)
	}
	return out, nil
}

func readme(o WriteOptions) string {
	return "# " + o.Suite + "\n\n" +
		"Support tasks against a copy of " + orDefault(o.Title, o.App) + ", generated by Heimdall from its OpenAPI file and policy.\n\n" +
		"- `services/" + o.App + "/`: the app copy. `mock.json` lists the operations; each is an HTTP route and an MCP tool over the state in each task's `seed/" + o.App + ".json`.\n" +
		"- `tasks/<id>/verifier/checks.yaml`: what each task checks (calls made, calls never made, the app's final state, the reply).\n" +
		"- Generated tasks start as pending review (`provenance.instruction_reviewed: false`). Accept or reject each one on the generator page, and run the readiness checks: they build the app image and prove each task's reference passes and its controls fail.\n\n" +
		"## Policy\n\n" + PolicyText(o.Rules)
}

// SetSuiteTasks sets suite.yaml's tasks list.
func SetSuiteTasks(src []byte, ids []string) ([]byte, error) {
	return editYAML(src, func(m *yaml.Node) {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		if len(ids) <= 6 {
			seq.Style = yaml.FlowStyle
		}
		for _, id := range ids {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id})
		}
		setNode(m, "tasks", seq)
	})
}

// SuiteTasks reads suite.yaml's tasks list.
func SuiteTasks(src []byte) ([]string, error) {
	var s struct {
		Tasks []string `yaml:"tasks"`
	}
	if err := yaml.Unmarshal(src, &s); err != nil {
		return nil, err
	}
	return s.Tasks, nil
}

func editYAML(src []byte, edit func(m *yaml.Node)) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("not a YAML mapping")
	}
	edit(doc.Content[0])
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

func setNode(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
}

func setScalar(m *yaml.Node, key, value string) {
	setNode(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
