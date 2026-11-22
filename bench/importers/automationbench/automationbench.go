// Package automationbench imports AutomationBench
// (https://github.com/zapier/AutomationBench, Zapier, MIT): business
// workflow tasks over 47 simulated SaaS apps (Gmail, Google Sheets, Slack,
// Salesforce, Zendesk, Jira, HubSpot and more), graded by assertions on the
// apps' final state. Each task runs upstream's simulated world, seeded with
// the task's initial state, as one MCP service; the world is exported after
// the agent exits and upstream's rubric scores it offline.
package automationbench

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	Name            = "automationbench"
	upstreamRepo    = "https://github.com/zapier/AutomationBench"
	upstreamLicense = "MIT"
	// UpstreamCommit is the commit the service image installs (v1.0.6,
	// 4 Aug 2026). The import records the revision of the export it read.
	UpstreamCommit  = "4a8e1061254004d9dac807054eed33fad7d1ff14"
	workspaceImage  = "python:3.12-slim"
	defaultImage    = "bench/automationbench:1"
	serviceID       = "apps"
	servicePort     = 8080
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	agentTimeoutS   = 1800
	verifierTimeout = 300
	worldExportMax  = 32 << 20
	// DefaultClock is the run clock of a task whose initial state sets no
	// current time (upstream then uses the wall clock): the day of the
	// upstream release, so every run of the task sees the same date.
	DefaultClock = "2026-08-04T09:00:00Z"
)

//go:embed image/Dockerfile
var serviceDockerfile string

//go:embed image/abench.py
var abenchPy string

//go:embed image/personas.py
var personasPy string

//go:embed image/refclient.py
var refClient string

//go:embed image/stubs/datasets/__init__.py
var stubDatasets string

//go:embed image/stubs/openai/__init__.py
var stubOpenAI string

// Toolsets the service offers (upstream --toolset).
const (
	ToolsetAPI     = "api"            // api_search, api_fetch, base64_encode (upstream default)
	ToolsetLimited = "limited_zapier" // the task's own Zapier tools
)

// Domains are upstream's task domains. simple is not part of upstream's score.
var Domains = []string{"sales", "marketing", "operations", "support", "finance", "hr", "simple"}

func init() { importers.Register(&importer{now: time.Now}) }

type importer struct{ now func() time.Time }

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"automationbench (600 public scored tasks in 6 domains, 200 simple tasks)"},
		Source:     "docker build -t " + defaultImage + " <suite>/services/automationbench; docker run --rm -v <ds>:/out " + defaultImage + " python /opt/abench/abench.py export --out /out/tasks.jsonl",
		License:    upstreamLicense,
		Track:      schemas.TrackToolUse,
		Grading:    schemas.GradingVerified,
		Needs:      []string{"the service image " + defaultImage + " built from services/automationbench (network at build time only)", "reference controls of your own: upstream ships none"},
		Summary:    "AutomationBench: business workflows over 47 simulated SaaS apps, graded by upstream's assertions on the final app state",
	}
}

// Row is one upstream task as `abench.py export` writes it.
type Row struct {
	Domain          string            `json:"domain"`
	ExampleID       int               `json:"example_id"`
	TaskName        string            `json:"task_name"`
	Prompt          []Message         `json:"prompt"`
	InitialState    json.RawMessage   `json:"initial_state"`
	Assertions      []json.RawMessage `json:"assertions"`
	ZapierTools     []string          `json:"zapier_tools"`
	AllowedServices []string          `json:"allowed_services"`
	ContractSHA256  string            `json:"contract_sha256"`
}

// Message is one prompt message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var idSafe = regexp.MustCompile(`[^a-z0-9]+`)

// TaskID is the bench id of an upstream task: ab-<domain>-<name without the
// domain prefix>, lower case, runs of other characters as "-".
func TaskID(domain, taskName string) string {
	s := strings.TrimPrefix(strings.ToLower(taskName), strings.ToLower(domain)+".")
	return "ab-" + domain + "-" + strings.Trim(idSafe.ReplaceAllString(s, "-"), "-")
}

type settings struct {
	domains    map[string]bool
	toolset    string
	image      string
	revision   string
	references string
	personas   string
}

func parseSettings(extra map[string]string) (settings, error) {
	st := settings{toolset: ToolsetAPI, image: defaultImage, domains: map[string]bool{}}
	for k, v := range extra {
		switch k {
		case "domains":
			for _, d := range strings.Split(v, ",") {
				if d = strings.TrimSpace(d); d == "" {
					continue
				} else if d == "public" {
					for _, p := range Domains[:6] {
						st.domains[p] = true
					}
				} else if !slices.Contains(Domains, d) {
					return st, fmt.Errorf("automationbench: unknown domain %q (%s, or public)", d, strings.Join(Domains, ", "))
				} else {
					st.domains[d] = true
				}
			}
		case "toolset":
			if v != ToolsetAPI && v != ToolsetLimited {
				return st, fmt.Errorf("automationbench: toolset %q is not %s or %s", v, ToolsetAPI, ToolsetLimited)
			}
			st.toolset = v
		case "image":
			st.image = v
		case "revision":
			st.revision = v
		case "references":
			st.references = v
		case "personas":
			st.personas = v
		default:
			return st, fmt.Errorf("automationbench: unknown setting %q (domains, toolset, image, revision, references, personas)", k)
		}
	}
	return st, nil
}

// Import implements importers.Importer. From holds tasks.jsonl, the output
// of `abench.py export` from the service image.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	st, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, err
	}
	rows, err := ReadRows(filepath.Join(opts.From, "tasks.jsonl"))
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	suiteDir := opts.SuiteDir()
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	seen := map[string]string{}
	for _, r := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(st.domains) > 0 && !st.domains[r.Domain] {
			continue
		}
		id := TaskID(r.Domain, r.TaskName)
		if len(want) > 0 && !want[id] && !want[r.TaskName] {
			continue
		}
		if opts.Limit > 0 && len(res.Tasks) >= opts.Limit {
			break
		}
		if prev, dup := seen[id]; dup {
			res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "task id also used by " + prev})
			continue
		}
		seen[id] = r.TaskName
		dir := filepath.Join(suiteDir, "tasks", id)
		if _, err := os.Stat(dir); err == nil {
			if !opts.Force {
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "exists (use -force)"})
				continue
			}
			_ = os.RemoveAll(dir)
		}
		if err := writeTask(st, dir, id, r); err != nil {
			_ = os.RemoveAll(dir)
			res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: err.Error()})
			continue
		}
		res.Tasks = append(res.Tasks, id)
	}
	if err := writeFiles(filepath.Join(suiteDir, "services", "automationbench"), map[string]string{
		"Dockerfile":                 serviceDockerfile,
		"abench.py":                  abenchPy,
		"personas.py":                personasPy,
		"stubs/datasets/__init__.py": stubDatasets,
		"stubs/openai/__init__.py":   stubOpenAI,
	}); err != nil {
		return nil, err
	}
	rev := st.revision
	if rev == "" {
		rev = UpstreamCommit
	}
	if err := writeSuite(suiteDir, opts.SuiteID, res.Tasks, rev, st, im.now()); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes,
		fmt.Sprintf("docker build -t %s %s", st.image, filepath.Join(suiteDir, "services", "automationbench")),
		"bifrost-bench suite lock "+opts.SuiteID,
		"tasks without a file in -set references=<dir> keep a refusing controls/reference.sh (upstream ships no solutions)")
	return res, importers.Finish(suiteDir)
}

// ReadRows reads the export.
func ReadRows(path string) ([]Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("automationbench: %w (write it with abench.py export)", err)
	}
	defer f.Close()
	var out []Row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 128<<20)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r Row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("automationbench: %s line %d: %w", path, n, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// currentTime is the current time the task's initial state sets, or "".
func currentTime(initial json.RawMessage) string {
	var s struct {
		Meta struct {
			CurrentTime string `json:"current_time"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(initial, &s)
	return s.Meta.CurrentTime
}

func writeTask(st settings, dir, id string, r Row) error {
	if len(r.Prompt) == 0 || r.Prompt[len(r.Prompt)-1].Role != "user" {
		return fmt.Errorf("prompt has no user message")
	}
	if len(r.Assertions) == 0 {
		return fmt.Errorf("no assertions")
	}
	if len(r.InitialState) == 0 || r.InitialState[0] != '{' {
		return fmt.Errorf("initial_state is not an object")
	}
	if st.toolset == ToolsetLimited && len(r.ZapierTools) == 0 {
		return fmt.Errorf("no zapier_tools for the limited_zapier toolset")
	}
	scenario, err := json.MarshalIndent(map[string]any{
		"task_name":        r.TaskName,
		"initial_state":    r.InitialState,
		"zapier_tools":     nonNil(r.ZapierTools),
		"allowed_services": nonNil(r.AllowedServices),
		"toolset":          st.toolset,
	}, "", " ")
	if err != nil {
		return err
	}
	grading, err := json.MarshalIndent(map[string]any{
		"task_name":       r.TaskName,
		"example_id":      r.ExampleID,
		"contract_sha256": r.ContractSHA256,
		"initial_state":   r.InitialState,
		"assertions":      r.Assertions,
	}, "", " ")
	if err != nil {
		return err
	}
	clock := currentTime(r.InitialState)
	if clock == "" {
		clock = DefaultClock
	}
	files := map[string]string{
		"scenario/scenario.json":      string(scenario),
		"verifier/tests/grading.json": string(grading),
		"repo/README.md":              "Workspace of AutomationBench task " + r.TaskName + ". The task's apps live behind the MCP server `" + serviceID + "`; files here are not part of the result.\n",
		"controls/empty.sh":           "#!/bin/sh\n# Empty control: no tool call, the apps stay as seeded.\nexit 0\n",
		"controls/exploit.sh":         "#!/bin/sh\n# Exploit control (must fail): claims success without any tool call.\necho 'All requested changes are done.'\nexit 0\n",
		"controls/reference.sh":       "#!/bin/sh\n# Reference control. AutomationBench ships no reference solutions, so this\n# task has none until one is written (a Python body in -set references=<dir>,\n# <task id>.py, run after refclient.py). Admission fails until then.\necho 'no reference solution for " + r.TaskName + "' >&2\nexit 1\n",
	}
	if st.references != "" {
		b, err := os.ReadFile(filepath.Join(st.references, id+".py"))
		if err == nil {
			files["controls/reference.sh"] = ReferenceScript(id, string(b))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	var people *personaDoc
	if st.personas != "" { // Z17 simulated colleagues (image/personas.py)
		b, err := os.ReadFile(filepath.Join(st.personas, id+".json"))
		if err == nil {
			if people, err = parsePersonas(b); err != nil {
				return fmt.Errorf("personas: %w", err)
			}
			files["scenario/personas.json"] = string(b)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	files["instruction.md"] = instruction(r, people)
	files["task.yaml"] = taskYAML(id, r, st, clock, people)
	return writeFiles(dir, files)
}

// ReferenceScript wraps a reference body (Python, using refclient.py's tool,
// get, post, patch, put, delete, send_email) as controls/reference.sh.
func ReferenceScript(id, body string) string {
	return "#!/bin/sh\n# Reference for AutomationBench task " + id + ", written by hand from its assertions (lane Z17).\nexec python3 - <<'PY'\n" +
		strings.TrimRight(refClient, "\n") + "\n# ---- task steps\n" + strings.TrimRight(body, "\n") + "\nPY\n"
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// personaDoc is the part of personas.json the importer reads (the service
// reads the rest).
type personaDoc struct {
	Mode     string `json:"mode"`
	Personas []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Role        string `json:"role"`
		Email       string `json:"email"`
		SlackUserID string `json:"slack_user_id"`
	} `json:"personas"`
}

func parsePersonas(b []byte) (*personaDoc, error) {
	var d personaDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	if d.Mode != "" && d.Mode != "scripted" && d.Mode != "llm" {
		return nil, fmt.Errorf("mode %q is not scripted or llm", d.Mode)
	}
	if len(d.Personas) == 0 {
		return nil, fmt.Errorf("no personas")
	}
	seen := map[string]bool{}
	for _, p := range d.Personas {
		if p.ID == "" || p.Name == "" || seen[p.ID] {
			return nil, fmt.Errorf("every persona needs a unique id and a name")
		}
		if p.Email == "" && p.SlackUserID == "" {
			return nil, fmt.Errorf("persona %s needs an email or a slack_user_id", p.ID)
		}
		seen[p.ID] = true
	}
	return &d, nil
}

func instruction(r Row, people *personaDoc) string {
	var b strings.Builder
	b.WriteString("You work through the MCP server `" + serviceID + "`: the business apps of this task (")
	b.WriteString(strings.Join(r.AllowedServices, ", "))
	b.WriteString("). Only the final state of their data is graded; your workspace and final message are not.\n\n")
	for _, m := range r.Prompt {
		if m.Role == "system" {
			b.WriteString("## Instructions\n\n" + strings.TrimSpace(m.Content) + "\n\n")
		}
	}
	b.WriteString("## Request\n\n" + strings.TrimSpace(r.Prompt[len(r.Prompt)-1].Content) + "\n")
	if people != nil {
		b.WriteString("\n## Colleagues\n\nThese people answer email and Slack messages you send them; their replies arrive in Gmail or in the Slack thread.\n\n")
		for _, p := range people.Personas {
			line := "- " + p.Name
			if p.Role != "" {
				line += ", " + p.Role
			}
			if p.Email != "" {
				line += ", " + p.Email
			}
			if p.SlackUserID != "" {
				line += ", Slack <@" + p.SlackUserID + ">"
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

func taskYAML(id string, r Row, st settings, clock string, people *personaDoc) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from AutomationBench by `bifrost-bench import automationbench`; see docs/importing-automationbench.md.\n")
	p("id: %s\nversion: %s\n", id, taskVersion)
	p("title: %s\n", q("AutomationBench "+r.Domain+": "+r.TaskName))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("automationbench-"+r.Domain))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: []\n  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", agentTimeoutS)
	tags := []string{q("automationbench"), q(r.Domain)}
	for _, s := range r.AllowedServices {
		tags = append(tags, q("app-"+s))
	}
	p("tags: [%s]\n", strings.Join(tags, ", "))
	p("capabilities: [tool_use, planning]\n")
	p("provenance:\n  source: %s\n  base_commit: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), UpstreamCommit, upstreamLicense, q(fmt.Sprintf("automationbench %s example %d (%s)", r.Domain, r.ExampleID, r.TaskName)), q("bifrost-bench import automationbench"))
	p("clock:\n  start: %s\n", q(clock))
	p("environment:\n  image: %s\n  verifier_image: %s\n", q(workspaceImage), q(st.image))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n    image: %s\n", serviceID, q(st.image))
	p("    argv: [python, /opt/abench/abench.py, serve, --scenario, /task/scenario.json, --state, /state, --port, %q]\n", fmt.Sprint(servicePort))
	p("    files:\n      /task/scenario.json: scenario/scenario.json\n")
	if people != nil {
		p("      /task/personas.json: scenario/personas.json\n")
	}
	p("    port: %d\n", servicePort)
	if people != nil && people.Mode == "llm" {
		// The colleagues' model calls go through the gateway on the service's
		// own virtual key, never the agent's (schemas.ServiceGateway).
		p("    gateway: {}\n")
	}
	p("    ready:\n      http: /healthz\n      timeout_s: 120\n")
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n      - id: world.json\n        path: /state/world.json\n        max_bytes: %d\n      - id: calls.json\n        path: /state/calls.json\n        max_bytes: %d\n", worldExportMax, worldExportMax)
	p("output:\n  kind: state\n")
	return b.String()
}

func writeSuite(dir, id string, tasks []string, rev string, st settings, at time.Time) error {
	all, err := existingTasks(dir)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from AutomationBench by `bifrost-bench import automationbench`; see docs/importing-automationbench.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("AutomationBench (Zapier, MIT) public tasks: business workflows over simulated SaaS apps (Gmail, Sheets, Slack, Salesforce, Zendesk, Jira, HubSpot and more) behind one MCP server per task, toolset "+st.toolset+". Upstream's assertions on the final app state decide, offline: reward is partial_credit, a pass needs every assertion. The official leaderboard uses a private held-out set, so scores are not comparable with it."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  tag: %s\n", q(workspaceImage))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python, /opt/abench/abench.py, verify, --tests, /grader/tests, --state, %s, --out, /grader/out]\n", "/grader/state/"+serviceID)
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: 1\n", verifierTimeout)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	p("track: %s\ngrading: %s\n", schemas.TrackToolUse, schemas.GradingVerified)
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		Name, q("automationbench"), q(upstreamRepo), q(rev), upstreamLicense, at.UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(b.String()), 0o644)
}

// existingTasks lists the task folders an earlier import left, so a partial
// re-import keeps them in suite.yaml.
func existingTasks(dir string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(dir, "tasks"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "tasks", e.Name(), "task.yaml")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func writeFiles(dir string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
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
	return nil
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
