// Package cooperbench imports CooperBench (arXiv 2601.13295) feature pairs
// as two-member team tasks: each member implements one feature of the same
// repository, they coordinate through the team mailbox, and upstream's
// per-feature tests grade the team's code. See docs/importing-cooperbench.md.
package cooperbench

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
)

//go:embed assets/verify.py
var verifyScript string

// Name is the import format name.
const Name = "cooperbench"

const (
	upstreamRepo    = "https://github.com/cooperbench/CooperBench"
	paperURL        = "https://arxiv.org/abs/2601.13295"
	license         = "MIT"
	registry        = "akhatua"
	defaultSuiteVer = "1.0.0"
	taskVersion     = "1.0.0"
	defaultAgentS   = 1800
	featureTimeoutS = 1200
)

// Settings (-set key=value).
const (
	setSubset   = "subset"   // all (default), lite, flash, core, ... (dataset/subsets/<name>.json)
	setRevision = "revision" // upstream commit when -from is not a git clone
	setVersion  = "version"  // suite version
	setAgentS   = "agent_s"  // agent wall time per task, seconds
)

var (
	versionRe  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	checkoutRe = regexp.MustCompile(`git checkout ([0-9a-f]{7,40})`)
	repoDirRe  = regexp.MustCompile(`^[a-z0-9_]+_task$`)
	taskDirRe  = regexp.MustCompile(`^task([0-9]+)$`)
)

// leakTerms mirror suite/lint.go: words an instruction must not hold.
var leakTerms = []string{"hidden test", "grader", "agents.md", "claude.md"}

type settings struct {
	subset, revision, version string
	agentS                    int
}

func parseSettings(extra map[string]string) (settings, error) {
	s := settings{subset: "all", version: defaultSuiteVer, agentS: defaultAgentS}
	for k, v := range extra {
		v = strings.TrimSpace(v)
		switch k {
		case setSubset:
			s.subset = v
		case setRevision:
			s.revision = v
		case setVersion:
			if !versionRe.MatchString(v) {
				return s, fmt.Errorf("-set %s=%s: want MAJOR.MINOR.PATCH", k, v)
			}
			s.version = v
		case setAgentS:
			n, err := strconv.Atoi(v)
			if err != nil || n < 60 || n > 14400 {
				return s, fmt.Errorf("-set %s=%s: want seconds between 60 and 14400", k, v)
			}
			s.agentS = n
		default:
			return s, fmt.Errorf("unknown -set %s (known: %s, %s, %s, %s)", k, setSubset, setRevision, setVersion, setAgentS)
		}
	}
	return s, nil
}

func init() { importers.Register(&importer{now: time.Now}) }

type importer struct{ now func() time.Time }

// Info implements importers.Importer.
func (im *importer) Info() importers.Info {
	return importers.Info{
		Name:       Name,
		Benchmarks: []string{"cooperbench (652 feature pairs, 30 tasks, 12 repositories)"},
		Source:     "git clone " + upstreamRepo + " (dataset/); paper " + paperURL,
		License:    license,
		Track:      schemas.TrackCoding,
		Grading:    schemas.GradingVerified,
		Needs: []string{
			"two gateway key slots per run (every task is a two-member team)",
			"the upstream task images " + registry + "/cooperbench-<repo>:task<id> (Docker Hub, multi-arch)",
		},
		Summary: "CooperBench feature pairs as two-member team tasks: one feature per member, the mailbox as upstream's messaging channel, upstream's runner.sh and tests per feature",
	}
}

// pair is one upstream feature pair of one task.
type pair struct {
	repo   string // upstream folder, e.g. go_chi_task
	taskID string // e.g. 26
	f1, f2 int
}

func (p pair) short() string {
	return strings.ReplaceAll(strings.TrimSuffix(p.repo, "_task"), "_", "-")
}
func (p pair) benchID() string {
	return fmt.Sprintf("%s-task%s-f%d-f%d", p.short(), p.taskID, p.f1, p.f2)
}
func (p pair) upstreamID() string {
	return fmt.Sprintf("%s/task%s/features_%d_%d", p.repo, p.taskID, p.f1, p.f2)
}
func (p pair) image() string {
	return fmt.Sprintf("%s/cooperbench-%s:task%s", registry, p.short(), p.taskID)
}

// datasetDir is the folder holding <repo>_task/ folders: -from itself or
// -from/dataset.
func datasetDir(from string) (string, error) {
	for _, d := range []string{filepath.Join(from, "dataset"), from} {
		if _, err := os.Stat(filepath.Join(d, "subsets")); err == nil {
			return d, nil
		}
	}
	return "", fmt.Errorf("%s: no dataset/subsets folder; clone %s", from, upstreamRepo)
}

func revision(from, set string) string {
	if set != "" {
		return set
	}
	out, err := exec.Command("git", "-C", from, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// subsetPairs reads dataset/subsets/<name>.json.
func subsetPairs(ds, name string) ([]pair, error) {
	if !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(name) {
		return nil, fmt.Errorf("-set %s=%s: not a subset name", setSubset, name)
	}
	b, err := os.ReadFile(filepath.Join(ds, "subsets", name+".json"))
	if err != nil {
		return nil, fmt.Errorf("subset %s: %w", name, err)
	}
	var doc struct {
		Tasks []struct {
			Repo   string  `json:"repo"`
			TaskID any     `json:"task_id"`
			Pairs  [][]int `json:"pairs"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("subset %s: %w", name, err)
	}
	var out []pair
	for _, t := range doc.Tasks {
		id := strings.TrimPrefix(fmt.Sprint(t.TaskID), "task")
		for _, p := range t.Pairs {
			if len(p) != 2 {
				continue
			}
			out = append(out, pair{repo: t.Repo, taskID: id, f1: p[0], f2: p[1]})
		}
	}
	return out, nil
}

// Import implements importers.Importer.
func (im *importer) Import(ctx context.Context, opts importers.Options) (*importers.Result, error) {
	set, err := parseSettings(opts.Extra)
	if err != nil {
		return nil, err
	}
	ds, err := datasetDir(opts.From)
	if err != nil {
		return nil, err
	}
	pairs, err := subsetPairs(ds, set.subset)
	if err != nil {
		return nil, err
	}
	rev := revision(opts.From, set.revision)
	suiteDir := opts.SuiteDir()
	if err := checkSuite(suiteDir); err != nil {
		return nil, err
	}
	res := &importers.Result{Suite: opts.SuiteID, SuiteDir: suiteDir}
	picked := pairs
	if len(opts.IDs) > 0 {
		picked = nil
		for _, id := range opts.IDs {
			n := 0
			for _, p := range pairs {
				if id == p.benchID() || id == p.upstreamID() || id == p.repo+"/task"+p.taskID {
					picked = append(picked, p)
					n++
				}
			}
			if n == 0 {
				res.Skipped = append(res.Skipped, importers.Skip{ID: id, Reason: "not a pair of subset " + set.subset})
			}
		}
	}
	if opts.Limit > 0 && len(picked) > opts.Limit {
		picked = picked[:opts.Limit]
	}
	for _, p := range picked {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dir := filepath.Join(suiteDir, "tasks", p.benchID())
		if _, err := os.Stat(dir); err == nil {
			if !opts.Force {
				res.Skipped = append(res.Skipped, importers.Skip{ID: p.upstreamID(), Reason: "task folder exists (use -force to replace it)"})
				continue
			}
			if err := os.RemoveAll(dir); err != nil {
				return nil, err
			}
		}
		files, skip, err := renderTask(ds, p, set, rev)
		if err != nil {
			return nil, fmt.Errorf("pair %s: %w", p.upstreamID(), err)
		}
		if skip != "" {
			res.Skipped = append(res.Skipped, importers.Skip{ID: p.upstreamID(), Reason: skip})
			continue
		}
		if err := writeFiles(dir, files); err != nil {
			return nil, err
		}
		res.Tasks = append(res.Tasks, p.benchID())
	}
	if len(res.Tasks) == 0 {
		return res, nil
	}
	if err := writeSuite(suiteDir, opts.SuiteID, set, rev, res.Tasks, ds, im.now()); err != nil {
		return nil, err
	}
	if err := importers.Finish(suiteDir); err != nil {
		return nil, err
	}
	res.Notes = []string{
		fmt.Sprintf("bifrost-bench suite lint %s && bifrost-bench suite lock %s && bifrost-bench suite admit %s", opts.SuiteID, opts.SuiteID, opts.SuiteID),
		"every task is a two-member team: a team run needs 2 free gateway key slots",
		"pull the upstream images first (" + registry + "/cooperbench-<repo>:task<id>); one image serves every pair of its task",
	}
	return res, nil
}

func checkSuite(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(b), "importer: "+Name+"\n") {
		return fmt.Errorf("%s/suite.yaml exists and was not written by the %s importer; pick another suite id", dir, Name)
	}
	return nil
}

func writeFiles(dir string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	if err := os.MkdirAll(filepath.Join(dir, "repo"), 0o755); err != nil {
		return err
	}
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

func read(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// renderTask builds one task folder's files, or a skip reason.
func renderTask(ds string, p pair, set settings, rev string) (map[string]string, string, error) {
	if !repoDirRe.MatchString(p.repo) || !regexp.MustCompile(`^[0-9]+$`).MatchString(p.taskID) || p.f1 == p.f2 || p.f1 < 1 || p.f2 < 1 {
		return nil, "malformed pair", nil
	}
	td := filepath.Join(ds, p.repo, "task"+p.taskID)
	dockerfile, err := read(filepath.Join(td, "Dockerfile"))
	if err != nil {
		return nil, "task has no Dockerfile", nil
	}
	m := checkoutRe.FindStringSubmatch(dockerfile)
	if m == nil {
		return nil, "the Dockerfile names no base commit (git checkout <sha>)", nil
	}
	base := m[1]
	runner, err := read(filepath.Join(td, "runner.sh"))
	if err != nil {
		return nil, "task has no runner.sh", nil
	}
	combined, err := read(filepath.Join(td, "combined.patch"))
	if err != nil || strings.TrimSpace(combined) == "" {
		return nil, "task has no combined.patch (the resolved reference for every pair)", nil
	}
	feat := map[int][3]string{}
	for _, n := range []int{p.f1, p.f2} {
		fd := filepath.Join(td, fmt.Sprintf("feature%d", n))
		md, e1 := read(filepath.Join(fd, "feature.md"))
		gold, e2 := read(filepath.Join(fd, "feature.patch"))
		tests, e3 := read(filepath.Join(fd, "tests.patch"))
		if e1 != nil || e2 != nil || e3 != nil || strings.TrimSpace(md) == "" || strings.TrimSpace(tests) == "" {
			return nil, fmt.Sprintf("feature%d lacks feature.md, feature.patch or tests.patch", n), nil
		}
		low := strings.ToLower(md)
		for _, t := range leakTerms {
			if strings.Contains(low, t) {
				return nil, fmt.Sprintf("feature%d text mentions %q, which suite lint refuses in an instruction", n, t), nil
			}
		}
		feat[n] = [3]string{md, gold, tests}
	}
	spec, _ := json.MarshalIndent(map[string]any{
		"base_commit": base, "pair": p.upstreamID(), "features": []int{p.f1, p.f2}, "feature_timeout_s": featureTimeoutS,
	}, "", "  ")
	files := map[string]string{
		"task.yaml":                      taskYAML(p, set, rev, base),
		"instruction.md":                 instruction(p, feat[p.f1][0], feat[p.f2][0]),
		"verifier/tests/verify.py":       verifyScript,
		"verifier/tests/runner.sh":       runner,
		"verifier/tests/spec.json":       string(spec) + "\n",
		"verifier/tests/tests1.patch":    feat[p.f1][2],
		"verifier/tests/tests2.patch":    feat[p.f2][2],
		"verifier/requirements.md":       requirements(p),
		"solution/reference.sh":          applyScript("Reference control: upstream's combined.patch (the whole PR, both features, conflicts resolved). In a team only the lead applies it; alone it is applied once.", combined, true),
		"controls/exploit.sh":            exploitScript,
		"controls/empty.sh":              "#!/bin/sh\n# Empty control (must fail): changes nothing.\nexit 0\n",
		"controls/mutant-one-feature.sh": applyScript(fmt.Sprintf("Mutant control (must fail): only feature %d's gold patch, so feature %d's tests fail.", p.f1, p.f2), feat[p.f1][1], true),
	}
	return files, "", nil
}

// exploitScript fakes success without implementing anything: it replaces
// the image's runner with one that prints a passing summary and plants a
// test file. It must fail: the verifier runs its own runner copy and drops
// test files from the team's diff.
const exploitScript = `#!/bin/sh
# Exploit control (must fail): fake a passing test run, implement nothing.
cat > /usr/local/bin/runner.sh 2>/dev/null <<'EOF' || true
#!/bin/sh
echo "5 passed in 0.01s"
exit 0
EOF
mkdir -p /workspace/repo/tests
printf 'def test_ok():\n    assert True\n' > /workspace/repo/tests/test_bench_exploit.py
exit 0
`

func applyScript(comment, patch string, leadOnly bool) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# " + comment + "\n# Written by `bifrost-bench import cooperbench` (docs/importing-cooperbench.md).\nset -e\n")
	if leadOnly {
		b.WriteString("if [ -n \"${HEIMDALL_AGENT_ID:-}\" ] && [ \"${HEIMDALL_AGENT_ID}\" != agent1 ]; then exit 0; fi\n")
	}
	b.WriteString("cd /workspace/repo\n")
	b.WriteString("git -c safe.directory='*' apply --whitespace=nowarn - <<'BENCH_COOPERBENCH_PATCH'\n")
	b.WriteString(patch)
	if !strings.HasSuffix(patch, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("BENCH_COOPERBENCH_PATCH\n")
	return b.String()
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func taskYAML(p pair, set settings, rev, base string) string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("# Imported from CooperBench by `bifrost-bench import cooperbench`; see docs/importing-cooperbench.md.\n")
	f("id: %s\nversion: %s\n", p.benchID(), taskVersion)
	f("title: %s\n", q(fmt.Sprintf("CooperBench %s task %s: features %d and %d as a two-member team", p.short(), p.taskID, p.f1, p.f2)))
	f("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("cooperbench-"+p.short()))
	f("verifier:\n  tests: verifier/tests\n  required: []\n")
	f("controls:\n  reference: solution/reference.sh\n  empty: controls/empty.sh\n  mutants: [controls/mutant-one-feature.sh]\n  exploit: controls/exploit.sh\n")
	f("timeouts:\n  agent_s: %d\n", set.agentS)
	f("tags: [cooperbench, multi_agent, %s, %s]\n", p.short(), "task"+p.taskID)
	f("capabilities: [coding, collaboration]\n")
	f("provenance:\n  source: %s\n  base_commit: %s\n  license: MIT\n  imported_from: %s\n  created_by: \"bifrost-bench import cooperbench\"\n",
		q(upstreamRepo), q(base), q(fmt.Sprintf("cooperbench@%s dataset/%s/task%s features %d,%d", short(rev), p.repo, p.taskID, p.f1, p.f2)))
	f("team:\n  - {id: agent1, role: lead}\n  - {id: agent2, role: worker}\n")
	f("environment:\n  image: %s\n  workspace_path: /workspace/repo\n  user: root\n", q(p.image()))
	return b.String()
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func instruction(p pair, f1, f2 string) string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("# Implement two features as a team (CooperBench %s task %s, features %d and %d)\n\n", p.short(), p.taskID, p.f1, p.f2)
	f("The repository is checked out in `/workspace/repo`. This task is for a team of two agents, `agent1` and `agent2`. ")
	f("Each member runs in its own container, on its own model credentials, and works in the same shared copy of the repository. ")
	f("Your own member id is in the environment variable `HEIMDALL_AGENT_ID`. If it is not set, you are working alone: implement both features yourself and skip the mailbox.\n\n")
	f("- `agent1` implements **Feature A** below.\n- `agent2` implements **Feature B** below.\n\n")
	f("Both features land in the same tree, and they may touch the same files, so coordinate to avoid clobbering each other's changes: ")
	f("at the start tell your partner which files and functions you intend to touch, read your messages after major edits and before you finish, ")
	f("and agree on one owner when you both need the same function. Your partner only knows what you tell them.\n\n")
	f("Messages go only through the team mailbox at the URL in `HEIMDALL_MAILBOX_URL` (your own route to it):\n\n")
	f("- send: `POST $HEIMDALL_MAILBOX_URL/send` with JSON `{\"from\": \"<your id>\", \"to\": \"<member id>\", \"body\": \"<text>\"}`\n")
	f("- receive: `POST $HEIMDALL_MAILBOX_URL/recv` with JSON `{\"agent\": \"<your id>\"}`; it returns `{\"messages\": [...]}`, each message once\n\n")
	f("Do not pass messages through files in the workspace. When you are done, leave your changes in the working tree (committing is optional). ")
	f("Change only source files: test files you write are dropped before grading, and do not edit existing tests.\n\n")
	f("## Feature A (agent1)\n\n%s\n\n## Feature B (agent2)\n\n%s\n", strings.TrimSpace(f1), strings.TrimSpace(f2))
	return b.String()
}

func requirements(p pair) string {
	return fmt.Sprintf("# What passes\n\nThe team's diff of `/workspace/repo` against the base commit (test files dropped, as upstream does) is run through upstream's `runner.sh` once with feature %d's tests and once with feature %d's tests. "+
		"A feature passes when the runner exits 0 and at least one test passed; the task passes when both do (upstream's `both_passed`).\n", p.f1, p.f2)
}

const baseDockerfile = `# Helper image for imported CooperBench tasks: the trusted steps need sh,
# cp and git. Each task runs and is verified in its own upstream image.
FROM python:3.12-slim@sha256:5e2bdb1b0d542d5039cffb8181c65930093949194a65c365dfb1b0fe13a4fa5b
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 agent \
 && mkdir -p /workspace /grader/out \
 && chown agent:agent /workspace /grader/out
ENV PYTHONDONTWRITEBYTECODE=1
WORKDIR /workspace
USER agent
`

func existingTasks(suiteDir string) []string {
	ents, err := os.ReadDir(filepath.Join(suiteDir, "tasks"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

func writeSuite(dir, id string, set settings, rev string, tasks []string, ds string, at time.Time) error {
	all := existingTasks(dir)
	for _, t := range tasks {
		if !slices.Contains(all, t) {
			all = append(all, t)
		}
	}
	sort.Strings(all)
	quoted := make([]string, len(all))
	for i, t := range all {
		quoted[i] = q(t)
	}
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	f("# Imported from CooperBench by `bifrost-bench import cooperbench`; see docs/importing-cooperbench.md.\n")
	f("# Upstream licence: MIT (see LICENSE.cooperbench and NOTICE.md in this folder).\n# subset: %s\n", set.subset)
	f("id: %s\nversion: %s\n", id, set.version)
	f("description: %s\n", q("CooperBench feature pairs as two-member team tasks: each member implements one feature of the same repository in a shared tree, the team mailbox is the messaging channel, and upstream's runner.sh and per-feature tests decide (both features must pass). Not comparable with the paper's scores: one shared tree instead of two merged patches."))
	f("environment: docker_workspace\ngrader: verifier_command\n")
	f("base_image:\n  dockerfile: base/Dockerfile\n  tag: %s\n", q("bench/"+id+"-base:1"))
	f("workdir: /workspace/repo\n")
	f("verifier:\n  command: [\"python3\", \"-B\", \"-I\", \"/grader/tests/verify.py\"]\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: 1\n  restore: []\n  timeout_s: %d\n  mode: snapshot\n", 2*featureTimeoutS+300)
	f("tasks: [%s]\nprobes: []\ntrack: %s\ngrading: %s\n", strings.Join(quoted, ", "), schemas.TrackCoding, schemas.GradingVerified)
	f("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n  revision: %s\n  license: %s\n  at: %s\n",
		Name, q("cooperbench/"+set.subset), q(upstreamRepo), q(rev), license, at.UTC().Format("2006-01-02T15:04:05Z"))
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), []byte(b.String()), 0o644); err != nil {
		return err
	}
	notice := "# Notice\n\nTasks in this suite come from CooperBench (" + upstreamRepo + ", revision " + rev + ", MIT licence, paper " + paperURL + "): feature descriptions, test patches, gold patches, combined patches and runner.sh are upstream's, unchanged. " +
		"The instructions, task.yaml files, verify.py and the control scripts were written by `bifrost-bench import cooperbench`. Task images are upstream's (" + registry + "/cooperbench-<repo>:task<id>).\n"
	if err := os.WriteFile(filepath.Join(dir, "NOTICE.md"), []byte(notice), 0o644); err != nil {
		return err
	}
	for _, cand := range []string{filepath.Join(ds, "..", "LICENSE"), filepath.Join(ds, "LICENSE")} {
		if lic, err := os.ReadFile(cand); err == nil {
			return os.WriteFile(filepath.Join(dir, "LICENSE.cooperbench"), lic, 0o644)
		}
	}
	return os.WriteFile(filepath.Join(dir, "LICENSE.cooperbench"), []byte("MIT (upstream "+upstreamRepo+"; LICENSE file not found in the source given to the importer)\n"), 0o644)
}
