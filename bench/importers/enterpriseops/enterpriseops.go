// Package enterpriseops imports EnterpriseOps-Gym
// (https://github.com/ServiceNow/EnterpriseOps-Gym, Apache-2.0; tasks from
// https://huggingface.co/datasets/ServiceNow-AI/EnterpriseOps-Gym, config
// oracle). Each task runs against upstream's MCP server image for its
// domain(s), seeded with the task's database snapshot, and is graded offline
// from the results of upstream's SQL checks, exported after the agent exits.
package enterpriseops

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	Name            = "enterpriseops"
	upstreamRepo    = "https://github.com/ServiceNow/EnterpriseOps-Gym"
	upstreamData    = "https://huggingface.co/datasets/ServiceNow-AI/EnterpriseOps-Gym"
	upstreamLicense = "Apache-2.0"
	upstreamImage   = "shivakrishnareddyma225/enterpriseops-gym-mcp-%s:latest"
	workspaceImage  = "python:3.12-slim"
	suiteVersion    = "1.0.0"
	taskVersion     = "1.0.0"
	agentTimeoutS   = 1800
	verifierTimeout = 300
	servicePort     = 8080
	imageTag        = "bench/eog-%s:1"
)

//go:embed image/Dockerfile
var serviceDockerfile string

//go:embed image/bench_front.py
var benchFront string

//go:embed image/runverify.py
var runVerify string

// domains maps upstream's MCP server names to the bench service id (the
// domain) and the upstream image's port and ASGI app.
var domains = map[string]struct {
	id, app string
	port    int
}{
	"gym-teams-mcp":        {"teams", "main:app", 8005},
	"sn-csm-server":        {"csm", "main:app", 8005},
	"gym-email-mcp":        {"email", "main:app", 8005},
	"gym-itsm-mcp":         {"itsm", "main:app", 8005},
	"gym-calendar":         {"calendar", "main:app", 8003},
	"sn-hr-internal":       {"hr", "main:app", 8005},
	"gym-google-drive-mcp": {"drive", "app.main:app", 8005},
}

func init() { importers.Register(&importer{now: time.Now}) }

type importer struct{ now func() time.Time }

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"enterpriseops-gym (oracle tool set, 649 public tasks)"},
		Source:     "git clone " + upstreamRepo + " repo; unzip repo/gym_dbs.zip -d dbs; " + upstreamData + " oracle/*.parquet converted to jsonl/<domain>.jsonl",
		License:    upstreamLicense,
		Track:      schemas.TrackMCP,
		Grading:    schemas.GradingVerified,
		Needs:      []string{"the domain images bench/eog-<domain>:1 built from services/eog-<domain> (FROM upstream's Docker Hub images)", "reference controls of your own: upstream ships none"},
		Summary:    "EnterpriseOps-Gym: stateful enterprise tasks over 7 domain MCP servers, graded by upstream SQL checks on the final database",
	}
}

type row struct {
	TaskID        string   `json:"task_id"`
	Domain        string   `json:"domain"`
	SystemPrompt  string   `json:"system_prompt"`
	UserPrompt    string   `json:"user_prompt"`
	SelectedTools []string `json:"selected_tools"`
	MCPEndpoint   string   `json:"mcp_endpoint"`
	Gyms          string   `json:"gym_servers_config"`
	Verifiers     string   `json:"verifiers"`
}

type gym struct {
	Name     string          `json:"mcp_server_name"`
	Seed     string          `json:"seed_database_file"`
	Context  map[string]any  `json:"context"`
	UserInfo json.RawMessage `json:"user_info"`
}

type check struct {
	Type   string          `json:"verifier_type"`
	Name   string          `json:"name"`
	Gym    string          `json:"gym_name"`
	Config json.RawMessage `json:"validation_config"`
	raw    json.RawMessage
}

var idSafe = regexp.MustCompile(`[^a-z0-9]+`)

// TaskID is the bench id of an upstream task: eog-<domain>-<upstream id
// without its task_ prefix>, lower case, runs of other characters as "-".
func TaskID(domain, upstream string) string {
	s := strings.TrimPrefix(strings.ToLower(upstream), "task_")
	return "eog-" + domain + "-" + strings.Trim(idSafe.ReplaceAllString(s, "-"), "-")
}

// Import implements importers.Importer. From holds jsonl/<domain>.jsonl
// (upstream's oracle parquet as JSON lines), dbs/ (gym_dbs.zip unpacked)
// and optionally repo/ (the clone, for the revision). Settings: domains
// (comma list, default all), revision.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	for k := range opts.Extra {
		if k != "domains" && k != "revision" {
			return nil, fmt.Errorf("enterpriseops: unknown setting %q (domains, revision)", k)
		}
	}
	files, err := filepath.Glob(filepath.Join(opts.From, "jsonl", "*.jsonl"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("enterpriseops: no %s/jsonl/*.jsonl (convert upstream's oracle parquet files)", opts.From)
	}
	sort.Strings(files)
	only := map[string]bool{}
	for _, d := range strings.Split(opts.Extra["domains"], ",") {
		if d = strings.TrimSpace(d); d != "" {
			only[d] = true
		}
	}
	want := map[string]bool{}
	for _, id := range opts.IDs {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}
	suiteDir := opts.SuiteDir()
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	used := map[string]bool{}
	for _, f := range files {
		rows, err := readRows(f)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(only) > 0 && !only[r.Domain] {
				continue
			}
			id := TaskID(r.Domain, r.TaskID)
			if len(want) > 0 && !want[id] && !want[r.TaskID] {
				continue
			}
			if opts.Limit > 0 && len(res.Tasks) >= opts.Limit {
				break
			}
			dir := filepath.Join(suiteDir, "tasks", id)
			if _, err := os.Stat(dir); err == nil {
				if !opts.Force {
					res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "exists (use -force)"})
					continue
				}
				_ = os.RemoveAll(dir)
			}
			svcs, err := im.writeTask(opts.From, dir, id, r)
			if err != nil {
				_ = os.RemoveAll(dir)
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: err.Error()})
				continue
			}
			for _, s := range svcs {
				used[s] = true
			}
			res.Tasks = append(res.Tasks, id)
		}
	}
	var svcNames []string
	for name, d := range domains {
		if !used[d.id] {
			continue
		}
		svcNames = append(svcNames, d.id)
		sd := filepath.Join(suiteDir, "services", "eog-"+d.id)
		df := strings.Replace(serviceDockerfile, "ARG UP_PORT=8005", fmt.Sprintf("ARG UP_PORT=%d", d.port), 1)
		df = strings.Replace(df, "ARG UP_APP=main:app", "ARG UP_APP="+d.app, 1)
		df = strings.Replace(df, "ARG UPSTREAM\n", "ARG UPSTREAM="+fmt.Sprintf(upstreamImage, d.id)+"\n", 1)
		if err := writeFiles(sd, map[string]string{"Dockerfile": df, "bench_front.py": benchFront}); err != nil {
			return nil, err
		}
		_ = name
	}
	sort.Strings(svcNames)
	rev := opts.Extra["revision"]
	if rev == "" {
		rev = gitHead(filepath.Join(opts.From, "repo"))
	}
	if err := writeSuite(suiteDir, opts.SuiteID, res.Tasks, rev, im.now()); err != nil {
		return nil, err
	}
	for _, s := range svcNames {
		res.Notes = append(res.Notes, fmt.Sprintf("docker build -t %s %s", fmt.Sprintf(imageTag, s), filepath.Join(suiteDir, "services", "eog-"+s)))
	}
	res.Notes = append(res.Notes, "bifrost-bench suite lock "+opts.SuiteID,
		"write controls/reference.sh per task (upstream ships no solutions) before admission")
	return res, importers.Finish(suiteDir)
}

func readRows(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r row
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("enterpriseops: %s: %w", path, err)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// writeTask writes one task folder and returns the service ids it uses.
func (im *importer) writeTask(from, dir, id string, r row) ([]string, error) {
	var gyms []gym
	if err := json.Unmarshal([]byte(r.Gyms), &gyms); err != nil {
		return nil, fmt.Errorf("gym_servers_config: %w", err)
	}
	var raws []json.RawMessage
	if err := json.Unmarshal([]byte(r.Verifiers), &raws); err != nil {
		return nil, fmt.Errorf("verifiers: %w", err)
	}
	if len(gyms) == 0 || len(raws) == 0 {
		return nil, fmt.Errorf("no gym server or no verifier")
	}
	if p := strings.TrimSpace(r.MCPEndpoint); p != "" && p != "/mcp" {
		return nil, fmt.Errorf("mcp_endpoint %q not supported", p)
	}
	byGym := map[string][]json.RawMessage{}
	for _, raw := range raws {
		var c check
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("verifier: %w", err)
		}
		if c.Type != "database_state" {
			return nil, fmt.Errorf("verifier type %q not supported (database_state only)", c.Type)
		}
		g := c.Gym
		if g == "" {
			g = gyms[0].Name
		}
		byGym[g] = append(byGym[g], raw)
	}
	files := map[string]string{}
	checks := map[string][]json.RawMessage{}
	var svcs []string
	var svcYAML strings.Builder
	var userInfo []string
	for _, g := range gyms {
		d, ok := domains[g.Name]
		if !ok {
			return nil, fmt.Errorf("unknown MCP server %q", g.Name)
		}
		for _, s := range svcs {
			if s == d.id {
				return nil, fmt.Errorf("MCP server %q listed twice", g.Name)
			}
		}
		svcs = append(svcs, d.id)
		seed, err := os.ReadFile(filepath.Join(from, "dbs", filepath.FromSlash(g.Seed)))
		if err != nil {
			return nil, fmt.Errorf("seed database: %w", err)
		}
		ctxJSON, _ := json.Marshal(nonNil(g.Context))
		tools := r.SelectedTools
		if tools == nil {
			tools = []string{}
		}
		toolsJSON, _ := json.Marshal(tools)
		mine := byGym[g.Name]
		delete(byGym, g.Name)
		if mine == nil {
			mine = []json.RawMessage{}
		}
		chk, _ := json.MarshalIndent(mine, "", " ")
		base := "service/" + d.id + "/"
		files[base+"seed.sql"] = string(seed)
		files[base+"context.json"] = string(ctxJSON)
		files[base+"tools.json"] = string(toolsJSON)
		files[base+"checks.json"] = string(chk)
		checks[d.id] = mine
		if s := strings.Trim(strings.TrimSpace(string(g.UserInfo)), `"`); s != "" && s != "null" {
			userInfo = append(userInfo, fmt.Sprintf("- `%s`: %s", d.id, string(g.UserInfo)))
		}
		fmt.Fprintf(&svcYAML, "  - id: %s\n    role: mcp\n    image: %s\n    port: %d\n", d.id, q(fmt.Sprintf(imageTag, d.id)), servicePort)
		fmt.Fprintf(&svcYAML, "    files:\n")
		for _, n := range []string{"seed.sql", "context.json", "tools.json", "checks.json"} {
			fmt.Fprintf(&svcYAML, "      /bench/task/%s: %s\n", n, base+n)
		}
		fmt.Fprintf(&svcYAML, "    ready:\n      http: /healthz\n      timeout_s: 600\n")
		fmt.Fprintf(&svcYAML, "    mcp:\n      transport: http\n      path: /mcp\n")
		fmt.Fprintf(&svcYAML, "    export:\n      - id: checks\n        argv: [python, /bench/bench_front.py, export]\n        max_bytes: %d\n", 16<<20)
	}
	if len(byGym) > 0 {
		return nil, fmt.Errorf("verifier names a gym the task does not start")
	}
	allChecks, _ := json.MarshalIndent(checks, "", " ")
	files["verifier/tests/checks.json"] = string(allChecks)
	files["verifier/tests/runverify.py"] = runVerify
	files["instruction.md"] = instruction(r, svcs, userInfo)
	files["repo/README.md"] = "Workspace of EnterpriseOps-Gym task " + r.TaskID + ". The task's data lives behind the MCP server(s) " + strings.Join(svcs, ", ") + "; files here are not part of the result.\n"
	files["controls/empty.sh"] = "#!/bin/sh\n# Empty control: no tool call, the database stays as seeded.\nexit 0\n"
	files["controls/exploit.sh"] = "#!/bin/sh\n# Exploit control (must fail): claims success without any tool call.\necho 'All requested changes are done.'\nexit 0\n"
	files["controls/reference.sh"] = "#!/bin/sh\n# Reference control. EnterpriseOps-Gym ships no reference solutions, so this\n# task has none until one is written: a script that solves the task through\n# the MCP endpoint(s) in $BENCH_MCP_SERVERS. Admission fails until then.\necho 'no reference solution for " + r.TaskID + "; write controls/reference.sh' >&2\nexit 1\n"
	files["task.yaml"] = taskYAML(id, r, svcYAML.String())
	return svcs, writeFiles(dir, files)
}

func nonNil(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func instruction(r row, svcs []string, userInfo []string) string {
	var b strings.Builder
	b.WriteString("You work through the MCP server(s) " + "`" + strings.Join(svcs, "`, `") + "`" + " (an enterprise " + r.Domain + " system). ")
	b.WriteString("Only the final state of their data is graded; your workspace and final message are not.\n\n")
	b.WriteString("## Policy\n\n" + strings.TrimSpace(r.SystemPrompt) + "\n\n")
	if len(userInfo) > 0 {
		b.WriteString("## Current user\n\n" + strings.Join(userInfo, "\n") + "\n\n")
	}
	b.WriteString("## Request\n\n" + strings.TrimSpace(r.UserPrompt) + "\n")
	return b.String()
}

func taskYAML(id string, r row, services string) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from EnterpriseOps-Gym by `bifrost-bench import enterpriseops`; see docs/importing-enterpriseops.md.\n")
	p("id: %s\nversion: %s\n", id, taskVersion)
	p("title: %s\n", q("EnterpriseOps-Gym "+r.Domain+": "+r.TaskID))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("enterpriseops-gym-"+r.Domain))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: []\n  exploit: controls/exploit.sh\n")
	p("timeouts:\n  agent_s: %d\n", agentTimeoutS)
	p("tags: [%s, %s]\n", q("enterpriseops-gym"), q(r.Domain))
	p("capabilities: [tool_use]\n")
	p("provenance:\n  source: %s\n  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(upstreamRepo), upstreamLicense, q("enterpriseops-gym oracle/"+r.Domain+" "+r.TaskID), q("bifrost-bench import enterpriseops"))
	p("environment:\n  image: %s\n", q(workspaceImage))
	p("services:\n%s", services)
	p("output:\n  kind: state\n")
	return b.String()
}

func writeSuite(dir, id string, tasks []string, rev string, at time.Time) error {
	quoted := make([]string, len(tasks))
	for i, t := range tasks {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from EnterpriseOps-Gym by `bifrost-bench import enterpriseops`; see docs/importing-enterpriseops.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("EnterpriseOps-Gym (ServiceNow, Apache-2.0), oracle tool set: each task runs upstream's domain MCP server(s) seeded with its database snapshot; upstream's SQL checks on the final database decide, offline. Upstream ships no reference solutions."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  tag: %s\n", q(workspaceImage))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python3, /grader/tests/runverify.py]\n")
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n", verifierTimeout)
	p("tasks: [%s]\n", strings.Join(quoted, ", "))
	p("probes: []\n")
	p("track: %s\ngrading: %s\n", schemas.TrackMCP, schemas.GradingVerified)
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		Name, q("enterpriseops-gym"), q(upstreamRepo), q(rev), upstreamLicense, at.UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(b.String()), 0o644)
}

func gitHead(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return "unknown"
	}
	s := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(s, "ref: "); ok {
		if h, err := os.ReadFile(filepath.Join(repo, ".git", filepath.FromSlash(ref))); err == nil {
			return strings.TrimSpace(string(h))
		}
		if f, err := os.Open(filepath.Join(repo, ".git", "packed-refs")); err == nil {
			defer f.Close()
			data, _ := io.ReadAll(f)
			for _, l := range strings.Split(string(data), "\n") {
				if strings.HasSuffix(l, " "+ref) {
					return strings.Fields(l)[0]
				}
			}
		}
		return "unknown"
	}
	return s
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
