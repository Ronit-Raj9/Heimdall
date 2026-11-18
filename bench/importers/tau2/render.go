package tau2

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// writer writes the suite and its task folders.
type writer struct {
	up         *upstream
	set        settings
	suiteID    string
	suiteDir   string
	dockerfile string                     // service Dockerfile with the upstream commit filled in
	serviceTag string                     // the service image tag the run builds, set by writeTask
	baseTag    string                     // bench/tau2-base:<hash of its Dockerfile>
	policies   map[string]string          // domain -> policy.md
	writes     map[string]map[string]bool // domain -> database-changing tools
}

// prepare reads the policies and the write tools, pins the upstream commit
// in the service Dockerfile and fixes the base image tag.
func (w *writer) prepare(domains []string) error {
	w.dockerfile = strings.ReplaceAll(serviceDockerfile, "__TAU2_REF__", w.up.revision)
	if w.set.user == UserLLM { // W3 customer support: the model-played user (bench/simuser) beside tau2svc.py
		w.dockerfile = strings.Replace(w.dockerfile, "COPY tau2svc.py /opt/tau2svc/tau2svc.py\n",
			"COPY tau2svc.py /opt/tau2svc/tau2svc.py\nCOPY simuser.py /opt/tau2svc/simuser.py\nCOPY prompts/ /opt/tau2svc/prompts/\n", 1)
	}
	w.baseTag = "bench/tau2-base:" + shortHash(baseDockerfile)
	w.writes = map[string]map[string]bool{}
	for _, d := range domains {
		p, err := w.up.policy(d)
		if err != nil {
			return err
		}
		w.policies[d] = p
		if w.writes[d], err = w.up.mutatingTools(d); err != nil {
			return err
		}
	}
	return nil
}

// writeTask writes one task folder, replacing what was there.
func (w *writer) writeTask(dir string, c candidate) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	acts, err := c.task.Criteria.actions()
	if err != nil {
		return err
	}
	scenario, err := scenarioJSON(c, w.set)
	if err != nil {
		return err
	}
	grading, err := json.MarshalIndent(map[string]string{
		"domain":             c.domain,
		"communicate_scorer": "substring",
		"nl_assertions":      w.set.nl,
	}, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{
		"instruction.md":              instruction(c.domain, w.policies[c.domain]),
		"repo/README.md":              "Workspace of tau2 " + c.domain + " task " + c.task.ID + ". The work happens through the tau2 MCP server; files here are not part of the result.\n",
		"workspace/Dockerfile":        baseDockerfile,
		"service/Dockerfile":          w.dockerfile,
		"service/tau2svc.py":          serviceScript,
		"scenario/scenario.json":      string(scenario) + "\n",
		"verifier/tests/task.json":    string(c.task.Raw) + "\n",
		"verifier/tests/grading.json": string(grading) + "\n",
		"verifier/requirements.md":    requirements(c),
		"controls/empty.sh":           "#!/bin/sh\n# Empty control: no tool call and no message to the customer.\nexit 0\n",
	}
	if w.set.user == UserLLM { // W3 customer support: the user simulator in the service image
		for rel, b := range simuser.Files() {
			files["service/"+rel] = string(b)
		}
	}
	for name, plan := range controlPlans(c, acts, w.writes[c.domain]) {
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
	// The run tags a task service's build by its content, the same way for
	// every task of the import; the verifier runs in that image.
	img, err := utils.BuildImageSpec(dir, "service/Dockerfile", "", "svc-"+w.suiteID+"-"+serviceID, true)
	if err != nil {
		return fmt.Errorf("service image tag: %w", err)
	}
	w.serviceTag = img.Tag
	return os.WriteFile(filepath.Join(dir, "task.yaml"), []byte(w.taskYAML(c, acts, img.Tag)), 0o644)
}

// scenarioJSON is what the service needs and nothing more: the domain, the
// starting state and the customer's scenario. The expected actions and the
// rest of the evaluation criteria go only to the verifier.
func scenarioJSON(c candidate, set settings) ([]byte, error) {
	task := map[string]any{"id": c.task.ID, "user_scenario": c.task.UserScenario}
	if c.task.InitialState != nil {
		task["initial_state"] = c.task.InitialState
	}
	return json.MarshalIndent(map[string]any{
		"domain":         c.domain,
		"user":           set.user,
		"max_user_turns": set.maxTurns,
		"task":           task,
	}, "", "  ")
}

// q quotes s as a YAML double-quoted scalar (JSON string syntax, which YAML
// reads the same way).
func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// taskYAML is the task file. The task's own image (workspace/Dockerfile) is
// the clean workspace the agent runs in; the verifier runs in the service
// image, which holds the upstream package. Both builds need the network.
func (w *writer) taskYAML(c candidate, acts []upAction, serviceImage string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from tau2-bench by `bifrost-bench import tau2`; see docs/importing-tau2.md.\n")
	p("id: %s\nversion: %s\n", c.id, taskVersion)
	p("title: %s\n", q(fmt.Sprintf("tau2 %s task %s", c.domain, c.task.ID)))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("tau2-"+c.domain))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n")
	if hasMutant(c, acts, w.writes[c.domain]) {
		p("  mutants: [controls/mutant1.sh]\n")
	} else {
		p("  mutants: []\n")
	}
	p("  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", agentTimeoutS)
	p("tags: [tau2, %s]\n", c.domain)
	p("capabilities: [tool_use]\n")
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), w.up.revision, upstreamLicense, q(fmt.Sprintf("tau2-bench %s task %s", c.domain, c.task.ID)), q("bifrost-bench import tau2"))
	p("environment:\n  dockerfile: workspace/Dockerfile\n  verifier_image: %s\n  build_network: true\n", q(serviceImage))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n", serviceID)
	p("    build:\n      dockerfile: service/Dockerfile\n      tag: %s\n", q(serviceImage))
	p("    argv: [python, /opt/tau2svc/tau2svc.py, serve, --scenario, /task/scenario.json, --state, /state, --port, %s]\n", q(strconv.Itoa(servicePort)))
	p("    files:\n      /task/scenario.json: scenario/scenario.json\n")
	p("    port: %d\n", servicePort)
	if w.set.user == UserLLM {
		// W3 customer support: gateway: {} marks the service as the simulated
		// user; in llm mode it gets its own virtual key for the suite's
		// simulator model, never the agent's.
		p("    gateway: {}\n")
	}
	p("    ready:\n      http: /healthz\n      timeout_s: 120\n")
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n")
	p("      - id: db.json\n        path: /state/db.json\n        max_bytes: %d\n", dbExportMax)
	p("      - id: transcript.json\n        path: /state/transcript.json\n")
	p("output:\n  kind: state\n")
	return b.String()
}

// writeSuite writes suite.yaml and the shared base image Dockerfile. Tasks of
// an earlier import stay listed.
func (w *writer) writeSuite(id string, tasks []string, rec schemas.ImportRecord) error {
	if err := os.MkdirAll(w.suiteDir, 0o755); err != nil {
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
	if err := os.MkdirAll(filepath.Join(w.suiteDir, "base"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(w.suiteDir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return err
	}
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from tau2-bench by `bifrost-bench import tau2`; see docs/importing-tau2.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("tau2-bench customer service domains run as MCP services with a scripted customer, graded on the final database. Not comparable with the upstream leaderboard."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  dockerfile: base/Dockerfile\n  tag: %s\n", q(w.baseTag))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python, /opt/tau2svc/tau2svc.py, verify, --tests, /grader/tests, --state, %s, --out, /grader/out]\n", "/grader/state/"+serviceID)
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n", verifierTimeout)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	if w.set.user == UserLLM {
		// W3 customer support: upstream's model-played user, pinned like a
		// judge; plan refuses to run until provider and model are pinned.
		p("track: %s\ngrading: %s\n", schemas.TrackSupport, schemas.GradingVerified)
		p("simulator:\n  mode: %s\n  provider: %s\n  model: %s\n  prompt_version: %s\n  max_turns: %d\n",
			schemas.SimulatorLLM, simuser.Placeholder, simuser.Placeholder, simuser.PromptTau2UserV1, w.set.maxTurns)
	} else {
		p("track: %s\ngrading: %s\n", schemas.TrackToolUse, schemas.GradingVerified)
	}
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		rec.Importer, q(rec.Benchmark), q(rec.Source), rec.Revision, rec.License, rec.At.Format("2006-01-02T15:04:05Z"))
	return os.WriteFile(filepath.Join(w.suiteDir, "suite.yaml"), []byte(b.String()), 0o644)
}

// instruction is the agent's task: upstream's agent instruction adapted to
// the respond_to_user tool, the conversation rule, and the domain policy.
func instruction(domain, policy string) string {
	var b strings.Builder
	b.WriteString("# Customer service: " + domain + "\n\n")
	b.WriteString("You are a customer service agent. Help the customer according to the policy below.\n\n")
	b.WriteString("## Tools and the conversation\n\n")
	b.WriteString("- Everything happens through the MCP server `" + serviceID + "`. Its tools read and change the " + domain + " database. Nothing else changes it.\n")
	b.WriteString("- The customer is reachable only through its tool `respond_to_user`: call it with your message; its result is the customer's reply.\n")
	b.WriteString("- Start by greeting the customer with `respond_to_user`. The customer then tells you what they need.\n")
	b.WriteString("- In each turn either send one message to the customer or make one tool call, not both.\n")
	b.WriteString("- The conversation is over when the customer's reply contains `###STOP###`, `###TRANSFER###` or `###OUT-OF-SCOPE###`. Then end your session.\n")
	b.WriteString("- Files in the workspace and your final message are not seen by the customer and are not part of the result.\n\n")
	b.WriteString("## Policy\n\n")
	b.WriteString(strings.TrimSpace(policy))
	b.WriteString("\n")
	return b.String()
}

// controlStep is one step of a control plan (see image/control.py).
type controlStep struct {
	Say       string          `json:"say,omitempty"`
	Call      string          `json:"call,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	EchoUser  bool            `json:"echo_user,omitempty"`
}

const greeting = "Hi! How can I help you today?"

// controlPlans builds the script controls of a task. reference performs the
// expected actions and tells the customer the required information. mutant1
// is a near miss: the reference without its last database change, or
// without the required information when it changes nothing. exploit claims
// success without acting and parrots the customer's own words back.
func controlPlans(c candidate, acts []upAction, writes map[string]bool) map[string][]controlStep {
	info := communicateInfo(c)
	build := func(drop int, withInfo bool) []controlStep {
		steps := []controlStep{{Say: greeting}}
		for i, a := range acts {
			if i == drop {
				continue
			}
			steps = append(steps, controlStep{Call: a.Name, Arguments: a.Arguments})
		}
		if withInfo && len(info) > 0 {
			steps = append(steps, controlStep{Say: "Here is the information you need: " + strings.Join(info, "; ") + "."})
		}
		return append(steps, controlStep{Say: "Is there anything else I can help you with?"})
	}
	plans := map[string][]controlStep{
		"reference": build(-1, true),
		"exploit": {
			{Say: greeting},
			{EchoUser: true},
			{Say: "Your request has been completed."},
		},
	}
	if last := lastWrite(acts, writes); last >= 0 {
		plans["mutant1"] = build(last, true)
	} else if len(info) > 0 {
		plans["mutant1"] = build(-1, false)
	}
	return plans
}

func hasMutant(c candidate, acts []upAction, writes map[string]bool) bool {
	_, ok := controlPlans(c, acts, writes)["mutant1"]
	return ok
}

func lastWrite(acts []upAction, writes map[string]bool) int {
	for i := len(acts) - 1; i >= 0; i-- {
		if writes[acts[i].Name] {
			return i
		}
	}
	return -1
}

// communicateInfo is the information the verifier checks for: upstream
// scores it only when COMMUNICATE is in the task's reward basis.
func communicateInfo(c candidate) []string {
	if !c.task.Criteria.has(basisCommunicate) {
		return nil
	}
	return c.task.Criteria.CommunicateInfo
}

var controlPurpose = map[string]string{
	"reference": "Reference control: performs the task's expected actions through the tau2 MCP endpoint and tells the customer the required information.",
	"mutant1":   "Mutant control (a near miss, must fail): the reference without its last database change, or without the required information.",
	"exploit":   "Exploit control (must fail): claims success without any tool call and repeats the customer's words back.",
}

// controlShell is a self-contained POSIX shell control: the shared client
// (image/control.py) and the task's plan, one JSON line, in here-documents.
func controlShell(name string, c candidate, plan []controlStep) (string, error) {
	doc, err := json.Marshal(map[string]any{"server": serviceID, "steps": plan})
	if err != nil {
		return "", err
	}
	if strings.Contains(controlScript, "\nPY\n") {
		return "", fmt.Errorf("control client holds the here-document marker")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# %s\n# tau2 %s task %s. Runs in the agent's place with the services up.\n", controlPurpose[name], c.domain, c.task.ID)
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

// requirements maps each reward dimension the verifier writes to what the
// instruction asks (verifier/requirements.md, read by suite lint).
func requirements(c candidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Requirements: tau2 %s task %s\n\n", c.domain, c.task.ID)
	b.WriteString("The verifier (`tau2svc.py verify`) writes these reward dimensions. The instruction asks the agent to help the customer according to the policy, through the `tau2` tools and `respond_to_user`.\n\n")
	b.WriteString("- `db`: the final " + c.domain + " database equals the database after the task's expected actions, replayed on a fresh copy (upstream's DB check). It covers what the customer asks for and what the policy allows.\n")
	if info := communicateInfo(c); len(info) > 0 {
		b.WriteString("- `communicate`: the agent's messages to the customer contain each of: ")
		quoted := make([]string, len(info))
		for i, s := range info {
			quoted[i] = q(s)
		}
		b.WriteString(strings.Join(quoted, ", ") + " (upstream's substring rule).\n")
	}
	if c.task.Criteria.has(basisNLAssertion) && len(c.task.Criteria.NLAssertions) > 0 {
		fmt.Fprintf(&b, "\nNot scored: %d natural-language assertion(s), which need a judge model.\n", len(c.task.Criteria.NLAssertions))
	}
	return b.String()
}
