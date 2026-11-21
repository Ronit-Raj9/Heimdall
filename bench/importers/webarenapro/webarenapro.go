// Package webarenapro imports WebArena-Pro (McGill-NLP/webarena-pro) from
// its native Harbor export (`make harbor-export`, one folder per split with
// a manifest.json) into a bench browser suite. See
// docs/importing-webarenapro.md.
//
// Each upstream task is a Docker Compose project: the WebArena-Pro runtime
// (a browser plus the WebArena-Pro MCP server and the grader) and one
// container per task website. The bench maps it onto services: the runtime
// is service `webarena` (role mcp, proxied, so every browser action is a
// ledger row), each website is a hidden data service the agent cannot
// reach, and the runtime's own grading (`webarena-pro-harbor finalize`) is
// the `webarena` service's state export. The hidden verifier re-checks the
// evidence exactly as the upstream tests/verify.py does.
package webarenapro

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Name is the importer's format name.
const Name = "webarenapro"

const (
	upstreamRepo    = "https://github.com/McGill-NLP/webarena-pro"
	upstreamLicense = "MIT"
	suiteVersion    = "1.0.0"
	baseImage       = "bench/coding-core-base:1"
	runtimeService  = "webarena"
	runtimePort     = 8000
	// finalizeTimeout is the upstream collect hook's grading timeout.
	finalizeTimeout = 1350
)

func init() { importers.Register(&importer{now: time.Now}) }

type importer struct{ now func() time.Time }

func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"WebArena-Pro (400 tasks: dev 100, test 300)"},
		Source:     "git clone " + upstreamRepo + ", then `make harbor-export` (docs/importing-webarenapro.md)",
		License:    upstreamLicense,
		Track:      schemas.TrackBrowser,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"the WebArena-Pro runtime image built from the repository (tagged as the manifest's runtime_image) and the webarenapro/<site> images",
			"an OpenAI-compatible judge for tasks whose rubric falls through to the upstream llm_judge tier (service gateway)",
			"a reference control per task: upstream ships none; one is generated only when every rubric node is decided by answer_match",
		},
		Summary: "WebArena-Pro: 20 self-hosted web apps driven through the upstream browser MCP runtime; graded by the runtime's own tiered rubric on the exported evidence",
	}
}

// settings are the -set keys.
type settings struct {
	split       string // dev, test or empty for every split found
	judgeModel  string // gateway model for the runtime's llm_judge tier; "off" disables
	judgeProv   string
	runtimeTag  string // overrides the manifest's runtime_image
	revision    string
	agentTimout int
}

func parseSettings(extra map[string]string) (settings, error) {
	s := settings{judgeModel: "gpt-6.2-sol", judgeProv: "openai"}
	for k, v := range extra {
		switch k {
		case "split":
			s.split = v
		case "judge_model":
			s.judgeModel = v
		case "judge_provider":
			s.judgeProv = v
		case "runtime_image":
			s.runtimeTag = v
		case "revision":
			s.revision = v
		case "agent_timeout":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return s, fmt.Errorf("agent_timeout: %q is not a positive number of seconds", v)
			}
			s.agentTimout = n
		default:
			return s, fmt.Errorf("unknown setting %q (split, judge_model, judge_provider, runtime_image, revision, agent_timeout)", k)
		}
	}
	return s, nil
}

// manifest is <split>/manifest.json of the Harbor export.
type manifest struct {
	Split        string `json:"split"`
	RuntimeImage string `json:"runtime_image"`
	Tasks        []struct {
		Task   string            `json:"task"`
		TaskID string            `json:"task_id"`
		Config string            `json:"config_sha256"`
		Images map[string]string `json:"images"`
	} `json:"tasks"`
}

// episode is the parts of environment/browser/episode.json the importer reads.
type episode struct {
	Task struct {
		Intent string   `json:"intent"`
		Sites  []string `json:"sites"`
		TaskID string   `json:"task_id"`
		Eval   struct {
			Nodes []struct {
				ID         string `json:"id"`
				Type       string `json:"type"`
				EvalConfig struct {
					Strategies []struct {
						Type   string         `json:"type"`
						Config map[string]any `json:"config"`
					} `json:"strategies"`
				} `json:"eval_config"`
			} `json:"nodes"`
		} `json:"eval"`
	} `json:"task"`
	URLs       map[string]string `json:"urls"`
	Images     map[string]string `json:"images"`
	WallSecond float64           `json:"wall_seconds"`
	Setup      float64           `json:"setup_timeout"`
}

type candidate struct {
	id      string // bench task id: <split>-<task_id>
	dir     string
	split   string
	runtime string
	config  string
}

var errNoTasks = errors.New("no WebArena-Pro task found (expected <from>/<split>/manifest.json from `make harbor-export`)")

func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	if opts.SuiteID == "" || opts.From == "" || opts.BenchRoot == "" {
		return nil, errors.New("webarenapro: bench root, -from and -suite are required")
	}
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, fmt.Errorf("webarenapro: %w", err)
	}
	cands, err := listTasks(opts.From, set)
	if err != nil {
		return nil, fmt.Errorf("webarenapro: %w", err)
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: opts.SuiteDir()}
	if len(opts.IDs) > 0 {
		var keep []candidate
		for _, c := range cands {
			if slices.Contains(opts.IDs, c.id) {
				keep = append(keep, c)
			}
		}
		cands = keep
	}
	if opts.Limit > 0 && len(cands) > opts.Limit {
		for _, c := range cands[opts.Limit:] {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: fmt.Sprintf("over -limit %d", opts.Limit)})
		}
		cands = cands[:opts.Limit]
	}
	refs := 0
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("webarenapro: %w", err)
		}
		dir := filepath.Join(res.SuiteDir, "tasks", c.id)
		if _, err := os.Stat(dir); err == nil && !opts.Force {
			res.Skipped = append(res.Skipped, importers.Skip{ID: c.id, Reason: "task folder exists (pass -force to replace it)"})
			continue
		}
		hasRef, err := writeTask(dir, c, set)
		if err != nil {
			return nil, fmt.Errorf("webarenapro: task %s: %w", c.id, err)
		}
		if hasRef {
			refs++
		}
		res.Tasks = append(res.Tasks, c.id)
	}
	rev := set.revision
	if rev == "" {
		rev = readRevision(opts.From)
	}
	rec := schemas.ImportRecord{Importer: Name, Benchmark: "webarena-pro", Source: upstreamRepo, Revision: rev,
		License: upstreamLicense, At: im.now().UTC().Truncate(time.Second)}
	if err := writeSuite(res.SuiteDir, opts.SuiteID, res.Tasks, rec); err != nil {
		return nil, fmt.Errorf("webarenapro: %w", err)
	}
	if err := importers.Finish(res.SuiteDir); err != nil {
		return nil, err
	}
	res.Notes = append(res.Notes,
		fmt.Sprintf("%d of %d tasks have a generated reference control (every rubric node decided by answer_match); the rest refuse in admission and stay drafts", refs, len(res.Tasks)),
		"tag the runtime image built by the repository as the manifest's runtime_image and pull the webarenapro/<site> images before admission",
		"bifrost-bench suite lock -suite "+opts.SuiteID+", then suite admit")
	return res, nil
}

// listTasks reads every split's manifest under from.
func listTasks(from string, set settings) ([]candidate, error) {
	splits := []string{"dev", "test"}
	if set.split != "" {
		splits = []string{set.split}
	}
	var out []candidate
	for _, sp := range splits {
		b, err := os.ReadFile(filepath.Join(from, sp, "manifest.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var m manifest
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("%s/manifest.json: %w", sp, err)
		}
		rt := m.RuntimeImage
		if set.runtimeTag != "" {
			rt = set.runtimeTag
		}
		for _, t := range m.Tasks {
			if !safeName.MatchString(t.Task) || !safeName.MatchString(t.TaskID) {
				return nil, fmt.Errorf("%s/manifest.json: bad task name %q", sp, t.Task)
			}
			out = append(out, candidate{id: sp + "-" + t.TaskID, dir: filepath.Join(from, sp, t.Task), split: sp, runtime: rt, config: t.Config})
		}
	}
	if len(out) == 0 {
		return nil, errNoTasks
	}
	return out, nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// readRevision reads HEAD of a .git next to the export (from/../.git or
// from/.git) without running git.
func readRevision(from string) string {
	for _, g := range []string{filepath.Join(from, ".git"), filepath.Join(from, "..", ".git"), filepath.Join(from, "..", "src", ".git")} {
		b, err := os.ReadFile(filepath.Join(g, "HEAD"))
		if err != nil {
			continue
		}
		h := strings.TrimSpace(string(b))
		if ref, ok := strings.CutPrefix(h, "ref: "); ok {
			r, err := os.ReadFile(filepath.Join(g, ref))
			if err != nil {
				continue
			}
			h = strings.TrimSpace(string(r))
		}
		return h
	}
	return ""
}

var agentTimeoutRE = regexp.MustCompile(`(?m)^\[agent\]\s*\ntimeout_sec\s*=\s*([0-9.]+)`)

func writeTask(dir string, c candidate, set settings) (bool, error) {
	epRaw, err := os.ReadFile(filepath.Join(c.dir, "environment", "browser", "episode.json"))
	if err != nil {
		return false, err
	}
	var ep episode
	if err := json.Unmarshal(epRaw, &ep); err != nil {
		return false, fmt.Errorf("episode.json: %w", err)
	}
	instr, err := os.ReadFile(filepath.Join(c.dir, "instruction.md"))
	if err != nil {
		return false, err
	}
	agentS := 1860
	if tb, err := os.ReadFile(filepath.Join(c.dir, "task.toml")); err == nil {
		if m := agentTimeoutRE.FindSubmatch(tb); m != nil {
			if f, err := strconv.ParseFloat(string(m[1]), 64); err == nil && f > 0 {
				agentS = int(f)
			}
		}
	}
	if set.agentTimout > 0 {
		agentS = set.agentTimout
	}
	answer, why := referenceAnswer(ep)

	if err := os.RemoveAll(dir); err != nil {
		return false, err
	}
	for _, d := range []string{"repo", "seed", "controls", "verifier/tests"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			return false, err
		}
	}
	token := shortHash("webarena-pro", c.id, c.config, string(epRaw))
	sites := slices.Clone(ep.Task.Sites)
	if len(sites) == 0 {
		for s := range ep.Images {
			sites = append(sites, s)
		}
	}
	sort.Strings(sites)

	// Services: the runtime, then one hidden data service per website.
	env := map[string]string{
		"WP_EVAL_TOKEN":                token,
		"WEBARENAPRO_LLM_JUDGE_STREAM": "0",
		"WEBARENA_PRO_ASR_API_KEY":     "",
	}
	for _, s := range sites {
		if u, ok := ep.URLs[s]; ok {
			env["WP_"+strings.ToUpper(strings.ReplaceAll(s, "-", "_"))+"_URL"] = u
		}
	}
	// The runtime's judge reads OpenAI-style variables; the service gateway
	// hands the service its own key, URL and pinned model.
	judgeEnv := `if [ -n "${BENCH_SVC_GATEWAY_URL:-}" ]; then export OPENAI_BASE_URL="$BENCH_SVC_GATEWAY_URL" OPENAI_API_KEY="$BENCH_SVC_GATEWAY_KEY" WEBARENAPRO_LLM_JUDGE_MODEL="${BENCH_SVC_MODEL:-` + set.judgeModel + `}"; fi; `
	runtime := schemas.ServiceSpec{
		ID: runtimeService, Role: "mcp", Image: c.runtime, Port: runtimePort,
		MCP:   &schemas.MCPEndpoint{Path: "/mcp"},
		Argv:  []string{"/bin/sh", "-c", judgeEnv + "exec webarena-pro-harbor supervise --config /opt/webarena/episode.json"},
		Env:   env,
		Files: map[string]string{"/opt/webarena/episode.json": "seed/episode.json"},
		Ready: &schemas.ReadyCheck{Argv: []string{"webarena-pro-harbor", "healthcheck"}, TimeoutS: max(600, int(ep.Setup))},
		Export: []schemas.StateExport{{ID: "result.json", MaxBytes: 64 << 20, Argv: []string{"/bin/sh", "-c",
			judgeEnv + fmt.Sprintf("webarena-pro-harbor finalize --timeout %d >&2; cat /webarena-evidence/result.json", finalizeTimeout)}}},
	}
	if set.judgeModel != "off" {
		runtime.Gateway = &schemas.ServiceGateway{Provider: set.judgeProv, Model: set.judgeModel}
	}
	services := []schemas.ServiceSpec{runtime}
	for _, s := range sites {
		img := ep.Images[s]
		if img == "" {
			return false, fmt.Errorf("site %s has no image", s)
		}
		services = append(services, schemas.ServiceSpec{ID: s, Role: "data", Hidden: true, Image: img,
			Env: map[string]string{"WP_EVAL_TOKEN": token}})
	}

	tags := []string{"split:" + c.split}
	for _, s := range sites {
		tags = append(tags, "site:"+s)
	}
	task := schemas.Task{
		ID: c.id, Version: "1.0.0", Title: clip(ep.Task.Intent, 120), Project: "webarena-pro", Kind: "feature",
		Instruction: "instruction.md", RepoDir: "repo", Services: services,
		Verifier:     schemas.TaskVerifier{Tests: "verifier/tests", Required: []string{}},
		Protected:    []string{},
		Controls:     schemas.TaskControls{Reference: "controls/reference.sh", Empty: "controls/empty.sh", Mutants: []string{}, Exploit: "controls/exploit.sh"},
		Timeouts:     schemas.TaskTimeouts{AgentS: agentS},
		Tags:         tags,
		Capabilities: []string{"tool_use", "browser"},
		Provenance: &schemas.TaskProvenance{Source: upstreamRepo + " (Harbor export " + c.split + "/" + filepath.Base(c.dir) + ")",
			License: upstreamLicense, CreatedBy: "bifrost-bench import webarenapro"},
	}
	ty, err := yaml.Marshal(task)
	if err != nil {
		return false, err
	}
	ref := refuseRef(why)
	if why == "" {
		ref = submitRef(answer)
	}
	files := map[string]string{
		"task.yaml":                "# Imported from WebArena-Pro by `bifrost-bench import webarenapro`; see docs/importing-webarenapro.md.\n" + string(ty),
		"instruction.md":           string(instr),
		"seed/episode.json":        string(epRaw),
		"repo/README.md":           "Scratch workspace: the work happens on the task websites through the webarena MCP server.\n",
		"verifier/tests/verify.py": verifyPy,
		"controls/reference.sh":    ref,
		"controls/empty.sh":        "#!/bin/sh\n# Empty control: no tool call, nothing submitted. Must fail.\nexit 0\n",
		"controls/exploit.sh":      exploitSh,
		"IMPORT.md":                importNote(c, sites, why),
	}
	for name, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
			return false, err
		}
	}
	return why == "", nil
}

// referenceAnswer builds an answer that passes every rubric node at its
// answer_match tier, or says why it cannot.
func referenceAnswer(ep episode) (string, string) {
	if len(ep.Task.Eval.Nodes) == 0 {
		return "", "the task has no rubric nodes"
	}
	var exact string
	var parts []string
	for _, n := range ep.Task.Eval.Nodes {
		var cfg map[string]any
		for _, s := range n.EvalConfig.Strategies {
			if s.Type == "answer_match" {
				cfg = s.Config
				break
			}
		}
		if n.Type != "tiered" || cfg == nil {
			return "", fmt.Sprintf("WebArena-Pro ships no reference solutions and rubric node %s is not decided by answer_match (it needs website state, browser events or the judge)", n.ID)
		}
		if v, ok := cfg["exact_match"]; ok {
			e := fmt.Sprint(v)
			if exact != "" && exact != e {
				return "", "two rubric nodes need different exact answers"
			}
			exact = e
		}
		if l, ok := cfg["must_include"].([]any); ok {
			for _, x := range l {
				parts = append(parts, fmt.Sprint(x))
			}
		}
		if l, ok := cfg["must_include_any"].([]any); ok && len(l) > 0 {
			parts = append(parts, fmt.Sprint(l[0]))
		}
	}
	if exact != "" {
		for _, p := range parts {
			if !strings.Contains(strings.ToLower(exact), strings.ToLower(p)) {
				return "", "an exact answer and a required phrase disagree"
			}
		}
		return exact, ""
	}
	if len(parts) == 0 {
		return "", "the answer_match rules name no expected text"
	}
	return strings.Join(parts, "; "), ""
}

func refuseRef(why string) string {
	return "#!/bin/sh\n# Reference control. " + strings.ReplaceAll(why, "\n", " ") + ".\n" +
		"echo 'no reference solution for this WebArena-Pro task' >&2\nexit 1\n"
}

// submitRef submits the expected answer through webarena_submit, over the
// same proxied MCP endpoint an agent uses.
func submitRef(answer string) string {
	a, _ := json.Marshal(answer)
	return "#!/bin/sh\n# Reference control: submits the answer the rubric's answer_match rules expect\n" +
		"# through webarena_submit, the way a correct agent finishes.\nexec python3 -I - <<'PY'\n" +
		"ANSWER = " + string(a) + "\n" + mcpClientPy + "\nPY\n"
}

const mcpClientPy = `import json, os, sys, urllib.request

servers = json.loads(os.environ.get("BENCH_MCP_SERVERS") or "{}")
URL = (servers.get("webarena") or {}).get("url") or "http://toolproxy:9200/s/webarena/mcp"
session = {}

def rpc(method, params=None, notify=False):
    msg = {"jsonrpc": "2.0", "method": method, "params": params or {}}
    if not notify:
        msg["id"] = rpc.n = getattr(rpc, "n", 0) + 1
    headers = {"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
    headers.update(session)
    req = urllib.request.Request(URL, data=json.dumps(msg).encode(), headers=headers, method="POST")
    with urllib.request.urlopen(req, timeout=300) as resp:
        if resp.headers.get("Mcp-Session-Id"):
            session["Mcp-Session-Id"] = resp.headers["Mcp-Session-Id"]
        body = resp.read().decode()
    if notify:
        return None
    if "data:" in body and not body.lstrip().startswith("{"):
        body = [l[5:] for l in body.splitlines() if l.startswith("data:")][-1]
    ans = json.loads(body)
    if "error" in ans:
        sys.exit(f"{method}: {ans['error']}")
    return ans["result"]

rpc("initialize", {"protocolVersion": "2025-03-26", "capabilities": {}, "clientInfo": {"name": "reference", "version": "1"}})
rpc("notifications/initialized", notify=True)
out = rpc("tools/call", {"name": "webarena_submit", "arguments": {"answer": ANSWER}})
if out.get("isError"):
    sys.exit("webarena_submit failed: " + json.dumps(out)[:500])
print("submitted")`

const exploitSh = `#!/bin/sh
# Exploit control (must fail): no submission; plants a fake evidence file and
# a fake reward in the workspace and claims success.
mkdir -p /workspace/webarena-evidence /workspace/logs/verifier 2>/dev/null
echo '{"score": 1, "rewards": {"reward": 1, "success": 1}}' > /workspace/webarena-evidence/result.json 2>/dev/null
echo '{"reward": 1}' > /workspace/logs/verifier/reward.json 2>/dev/null
echo 'Task complete. Final answer submitted.'
exit 0
`

// verifyPy is upstream tests/verify.py (webarena-pro-harbor/verify.py)
// reading the exported evidence and writing the bench reward.
const verifyPy = `"""WebArena-Pro verifier: upstream tests/verify.py on the exported evidence.

The runtime grades the episode itself (webarena-pro-harbor finalize, the
state export of service webarena). This re-checks the evidence exactly as
upstream does and writes the bench reward. Missing, incomplete or erroneous
evidence is an error, never a reward.
"""
import json
import math
import sys
from pathlib import Path

SOURCE = Path("/grader/state/webarena/result.json")
OUT = Path("/grader/out/reward.json")


def main():
    result = json.loads(SOURCE.read_text(encoding="utf-8"))
    if not isinstance(result, dict):
        raise ValueError("evidence is not an object")
    if result.get("error"):
        raise ValueError("WebArena-Pro evaluation error: %s" % str(result["error"])[:500])
    rewards = result.get("rewards")
    if not isinstance(rewards, dict) or not {"reward", "success"} <= rewards.keys():
        raise ValueError("Missing Core reward/success metrics")
    for name, value in rewards.items():
        if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
            raise ValueError("bad metric %s" % name)
    if rewards["success"] not in (0, 1) or rewards["success"] != int(rewards["reward"] == 1):
        raise ValueError("Core success and partial-credit reward disagree")
    if rewards["reward"] != result.get("score"):
        raise ValueError("Core score and reward disagree")
    OUT.parent.mkdir(parents=True, exist_ok=True)
    OUT.write_text(json.dumps({"reward": float(rewards["reward"])}) + "\n")
    (OUT.parent / "webarena-rewards.json").write_text(json.dumps(rewards) + "\n")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:  # noqa: BLE001
        print("verify: %s" % exc, file=sys.stderr)
        sys.exit(1)
`

func importNote(c candidate, sites []string, why string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Import notes: %s\n\n", c.id)
	fmt.Fprintf(&b, "- Upstream: %s, Harbor export `%s/%s` (config sha256 %s).\n", upstreamRepo, c.split, filepath.Base(c.dir), c.config)
	fmt.Fprintf(&b, "- Runtime image `%s` (service `webarena`, MCP at /mcp); websites as hidden services: %s.\n", c.runtime, strings.Join(sites, ", "))
	b.WriteString("- Grading: the runtime's own tiered rubric (`webarena-pro-harbor finalize`) is the export of service webarena; `verifier/tests/verify.py` is upstream tests/verify.py on it.\n")
	if why != "" {
		b.WriteString("- Reference: none. " + why + ". The task stays a draft until a reference is written.\n")
	} else {
		b.WriteString("- Reference: generated; submits the text every answer_match rule expects.\n")
	}
	return b.String()
}

func writeSuite(dir, id string, tasks []string, rec schemas.ImportRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	all := []string{}
	if b, err := os.ReadFile(filepath.Join(dir, "suite.yaml")); err == nil {
		var s schemas.Suite
		if err := yaml.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("read existing suite.yaml: %w", err)
		}
		all = s.TaskIDs
	}
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from WebArena-Pro by `bifrost-bench import webarenapro`; see docs/importing-webarenapro.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("WebArena-Pro: tasks on 20 self-hosted web apps, driven through the upstream WebArena-Pro browser MCP runtime and graded by its tiered rubric on the exported evidence."))
	p("track: %s\ngrading: %s\n", schemas.TrackBrowser, schemas.GradingVerified)
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  tag: %s\nworkdir: /workspace\n", q(baseImage))
	p("output:\n  kind: state\n")
	p("verifier:\n  command: [python3, /grader/tests/verify.py]\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: 1\n  restore: []\n  timeout_s: 120\n")
	p("tasks: [%s]\nprobes: []\n", strings.Join(quoted, ", "))
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %q\n  license: %s\n  at: %s\n",
		rec.Importer, q(rec.Benchmark), q(rec.Source), rec.Revision, rec.License, rec.At.Format("2006-01-02T15:04:05Z"))
	return os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(b.String()), 0o644)
}

func shortHash(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-3]) + "..."
}
