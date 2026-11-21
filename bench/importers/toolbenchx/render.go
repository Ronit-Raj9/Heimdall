package toolbenchx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

type writer struct {
	revision   string // dataset commit
	variant    string
	suiteID    string
	suiteDir   string
	baseTag    string
	serviceTag string
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// writeTask writes one task folder, replacing what was there. The service/
// folder is the same for every task (one image per import); the task's tool
// module and scenario are copied into the service container at run time.
func (w *writer) writeTask(dir string, c candidate) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	tools, err := os.ReadFile(c.toolsPath)
	if err != nil {
		return err
	}
	scenario, err := json.MarshalIndent(map[string]any{
		"task":        c.id,
		"user_prompt": c.prompt,
		"tools_used":  c.tools,
		"variant":     w.variant,
	}, "", "  ")
	if err != nil {
		return err
	}
	expected, err := json.MarshalIndent(map[string]any{
		"task":         c.id,
		"upstream":     c.taskType + "/" + c.topic + "/" + c.subtopic + "/" + c.upID,
		"final_answer": c.answer,
		"hazard":       c.hazard,
	}, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{
		"instruction.md":               instruction(c),
		"repo/README.md":               "Workspace of ToolBench-X task " + c.id + ". The work happens through the toolbench MCP server; files here are not part of the result.\n",
		"workspace/Dockerfile":         baseDockerfile,
		"service/Dockerfile":           serviceDockerfile,
		"service/tbxsvc.py":            serviceScript,
		"scenario/scenario.json":       string(scenario) + "\n",
		"scenario/tools.py":            string(tools),
		"verifier/tests/expected.json": string(expected) + "\n",
		"verifier/requirements.md":     requirements(c),
		"controls/empty.sh":            "#!/bin/sh\n# Empty control: no tool call and no answer.\nexit 0\n",
	}
	for name, plan := range controlPlans(c) {
		s, err := controlShell(name, c, plan)
		if err != nil {
			return err
		}
		files["controls/"+name+".sh"] = s
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(files[n]), mode); err != nil {
			return err
		}
	}
	img, err := utils.BuildImageSpec(dir, "service/Dockerfile", "", "svc-"+w.suiteID+"-"+serviceID, true)
	if err != nil {
		return fmt.Errorf("service image tag: %w", err)
	}
	w.serviceTag = img.Tag
	return os.WriteFile(filepath.Join(dir, "task.yaml"), []byte(w.taskYAML(c, img.Tag)), 0o644)
}

func (w *writer) taskYAML(c candidate, serviceImage string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from ToolBench-X by `bifrost-bench import toolbench-x`; see docs/importing-toolbench-x.md.\n")
	p("id: %s\nversion: %s\n", c.id, taskVersion)
	p("title: %s\n", q(fmt.Sprintf("ToolBench-X %s %s / %s %s", c.taskType, c.topic, c.subtopic, c.upID)))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("toolbench-x-"+slug(c.topic)))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: [controls/mutant1.sh]\n  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", agentTimeoutS)
	tags := []string{"toolbench-x", c.taskType, slug(c.topic), w.variant}
	if c.hazard != "" {
		tags = append(tags, slug(c.hazard))
	}
	p("tags: [%s]\n", strings.Join(tags, ", "))
	p("capabilities: [tool_use]\n")
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), w.revision, q(upstreamLicense), q(fmt.Sprintf("ToolBench-X %s/%s/%s %s", c.taskType, c.topic, c.subtopic, c.upID)), q("bifrost-bench import toolbench-x"))
	p("environment:\n  dockerfile: workspace/Dockerfile\n  verifier_image: %s\n  build_network: true\n", q(serviceImage))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n", serviceID)
	p("    build:\n      dockerfile: service/Dockerfile\n      tag: %s\n", q(serviceImage))
	p("    argv: [python, /opt/tbxsvc/tbxsvc.py, serve, --scenario, /task/scenario.json, --tools, /task/tools.py, --state, /state, --port, %s]\n", q(strconv.Itoa(servicePort)))
	p("    files:\n      /task/scenario.json: scenario/scenario.json\n      /task/tools.py: scenario/tools.py\n")
	p("    port: %d\n", servicePort)
	p("    ready:\n      http: /healthz\n      timeout_s: 120\n")
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n")
	p("      - id: answer.json\n        path: /state/answer.json\n")
	p("      - id: transcript.json\n        path: /state/transcript.json\n")
	p("output:\n  kind: state\n")
	return b.String()
}

func (w *writer) writeSuite(id string, tasks []string, rec schemas.ImportRecord) error {
	if err := os.MkdirAll(filepath.Join(w.suiteDir, "base"), 0o755); err != nil {
		return err
	}
	all, err := existingTasks(w.suiteDir)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	if err := os.WriteFile(filepath.Join(w.suiteDir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return err
	}
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	desc := "ToolBench-X multi-step tool-use tasks: each task's upstream tools served as an MCP service through the tool proxy, graded by exact match on the submitted answer."
	if w.variant == VariantHazard {
		desc = "ToolBench-X hazard variant: the same tasks with upstream's recoverable tool faults (specification, invocation, execution, output and cross-source hazards), graded by exact match."
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from ToolBench-X by `bifrost-bench import toolbench-x`; see docs/importing-toolbench-x.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q(desc))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  dockerfile: base/Dockerfile\n  tag: %s\n", q(w.baseTag))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python, /opt/tbxsvc/tbxsvc.py, verify, --tests, /grader/tests, --state, %s, --out, /grader/out]\n", "/grader/state/"+serviceID)
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n", verifierTimeout)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	p("track: %s\ngrading: %s\n", schemas.TrackToolUse, schemas.GradingVerified)
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		rec.Importer, q(rec.Benchmark), q(rec.Source), q(rec.Revision), q(rec.License), rec.At.Format("2006-01-02T15:04:05Z"))
	return os.WriteFile(filepath.Join(w.suiteDir, "suite.yaml"), []byte(b.String()), 0o644)
}

func instruction(c candidate) string {
	var b strings.Builder
	b.WriteString("# Tool use: answer the request\n\n")
	b.WriteString("Answer the user's request below using the tools of the MCP server `" + serviceID + "`.\n\n")
	b.WriteString("## Request\n\n")
	b.WriteString(strings.TrimSpace(c.prompt) + "\n\n")
	b.WriteString("## Rules\n\n")
	b.WriteString("- Get the facts from the tools; do not guess. Tools take the request text as `user_prompt` where they ask for it, and may need results of earlier calls as arguments.\n")
	b.WriteString("- Tools can fail, time out, change their output shape or disagree with each other. Check each result, retry or work around failures, and do not invent missing values.\n")
	b.WriteString("- When you have the answer, call `submit_answer` with only the exact value: a number, a name or a short phrase, with no units, currency symbols, labels or explanation unless the request asks for them. The last submission counts.\n")
	b.WriteString("- Files in the workspace and your final message are not part of the result; only `submit_answer` is graded (exact match).\n")
	return b.String()
}

type controlStep struct {
	Call      string         `json:"call,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Retries   int            `json:"retries,omitempty"`
	Submit    *string        `json:"submit,omitempty"`
}

// mutateAnswer is a near miss of the expected answer.
func mutateAnswer(a string) string {
	if n, err := strconv.ParseFloat(a, 64); err == nil {
		if strings.Contains(a, ".") {
			return strconv.FormatFloat(n+1, 'f', len(a)-strings.Index(a, ".")-1, 64)
		}
		return strconv.FormatInt(int64(n)+1, 10)
	}
	return a + " (unverified)"
}

// controlPlans: reference calls every listed tool with the request (retrying
// failures) and submits the expected answer; mutant1 does the same but
// submits a near miss; exploit submits the request text itself.
func controlPlans(c candidate) map[string][]controlStep {
	calls := make([]controlStep, 0, len(c.tools))
	for _, t := range c.tools {
		calls = append(calls, controlStep{Call: t, Arguments: map[string]any{"user_prompt": c.prompt}, Retries: 3})
	}
	ref, mut, exp := c.answer, mutateAnswer(c.answer), c.prompt
	return map[string][]controlStep{
		"reference": append(slices.Clone(calls), controlStep{Submit: &ref}),
		"mutant1":   append(slices.Clone(calls), controlStep{Submit: &mut}),
		"exploit":   {{Submit: &exp}},
	}
}

var controlPurpose = map[string]string{
	"reference": "Reference control: calls the task's tools through the toolbench MCP endpoint and submits the expected answer.",
	"mutant1":   "Mutant control (a near miss, must fail): the reference with a slightly wrong answer.",
	"exploit":   "Exploit control (must fail): submits the request text as the answer without calling a tool.",
}

func controlShell(name string, c candidate, plan []controlStep) (string, error) {
	doc, err := json.Marshal(map[string]any{"server": serviceID, "steps": plan})
	if err != nil {
		return "", err
	}
	if strings.Contains(controlScript, "\nPY\n") || strings.Contains(string(doc), "\nJSON\n") {
		return "", fmt.Errorf("control holds a here-document marker")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# %s\n# ToolBench-X task %s. Runs in the agent's place with the services up.\n", controlPurpose[name], c.id)
	b.WriteString("set -eu\nclient=$(mktemp)\ntrap 'rm -f \"$client\"' EXIT\ncat > \"$client\" <<'PY'\n")
	b.WriteString(controlScript)
	if !strings.HasSuffix(controlScript, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("PY\npython3 \"$client\" <<'JSON'\n")
	b.Write(doc)
	b.WriteString("\nJSON\n")
	return b.String(), nil
}

func requirements(c candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Requirements: ToolBench-X task %s\n\n", c.id)
	b.WriteString("The verifier (`tbxsvc.py verify`) writes one reward dimension. The instruction asks the agent to answer the request with the `toolbench` tools and submit the exact value with `submit_answer`.\n\n")
	b.WriteString("- `exact_match`: the submitted answer, stripped, equals the task's final_answer, stripped (upstream's rule in agent/evaluation.py). No answer scores 0.\n")
	return b.String()
}
