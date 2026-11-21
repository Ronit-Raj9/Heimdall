package balrog

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

//go:embed image/Dockerfile
var imageDockerfile string

//go:embed image/base.Dockerfile
var baseDockerfile string

//go:embed image/control.py
var controlScript string

//go:embed image/heimdall/*.py
var heimdallFS embed.FS

type writer struct {
	up       *upstream
	suiteDir string
	imageTag string // bench/balrog:<package version>-<hash of the image context>
	drafts   int
}

// writeImage writes the image build context into the suite and names the
// image by a hash of it.
func (w *writer) writeImage() error {
	files, err := w.up.packageTree()
	if err != nil {
		return err
	}
	ctx := map[string][]byte{"Dockerfile": []byte(imageDockerfile)}
	for p, b := range files {
		ctx["upstream/"+p] = b
	}
	err = fs.WalkDir(heimdallFS, "image/heimdall", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := heimdallFS.ReadFile(p)
		if err != nil {
			return err
		}
		ctx["heimdall/"+path.Base(p)] = b
		return nil
	})
	if err != nil {
		return fmt.Errorf("embedded image scripts: %w", err)
	}
	dir := filepath.Join(w.suiteDir, "image")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	h := sha256.New()
	for _, n := range sortedNames(ctx) {
		h.Write([]byte(n))
		h.Write([]byte{0})
		h.Write(ctx[n])
		h.Write([]byte{0})
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(n)), ctx[n], 0o644); err != nil {
			return err
		}
	}
	w.imageTag = "bench/balrog:" + w.up.version + "-" + hex.EncodeToString(h.Sum(nil))[:12]
	return nil
}

// draftReason says why an episode has no reference control, or "".
func draftReason(e episode) string {
	if e.Error != "" {
		return "the environment does not build in the BALROG image: " + e.Error
	}
	if e.Reference == nil {
		if e.ReferenceNote != "" {
			return e.ReferenceNote
		}
		return "no reference action sequence"
	}
	return ""
}

func (w *writer) writeTask(dir, id string, e episode) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	expected, err := json.MarshalIndent(map[string]any{"env": e.Env, "task": e.Task, "seed": e.Seed}, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{
		"instruction.md":           instruction(e),
		"repo/README.md":           "Workspace of BALROG " + e.Env + " " + e.Task + " (seed " + fmt.Sprint(e.Seed) + "). The game is played through the MCP server `" + serviceID + "`; nothing here is graded.\n",
		"verifier/tests/task.json": string(expected) + "\n",
		"verifier/requirements.md": requirements(e),
		"controls/empty.sh":        "#!/bin/sh\n# Empty control: no action, so upstream's progression stays 0 and the task fails.\nexit 0\n",
		"controls/exploit.sh":      exploitShell,
	}
	mutants := false
	if reason := draftReason(e); reason != "" {
		files["controls/reference.sh"] = "#!/bin/sh\n# No reference control for this episode, so it stays a draft.\necho " + shQuote("no reference: "+reason) + " >&2\nexit 1\n"
	} else {
		ref, err := controlShell("Reference control: an action sequence that reaches progression 1", e, e.Reference)
		if err != nil {
			return err
		}
		files["controls/reference.sh"] = ref
		if e.MutantFails && len(e.Reference) > 0 {
			mut, err := controlShell("Mutant control: the reference without its last action, a near miss that must fail", e, e.Reference[:len(e.Reference)-1])
			if err != nil {
				return err
			}
			files["controls/mutant1.sh"] = mut
			mutants = true
		}
	}
	files["task.yaml"] = w.taskYAML(id, e, mutants)
	for _, n := range sortedNames(files) {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(n, ".sh") {
			mode = 0o755
		}
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(n)), []byte(files[n]), mode); err != nil {
			return err
		}
	}
	return nil
}

func (w *writer) taskYAML(id string, e episode, mutant ...bool) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from BALROG by `bifrost-bench import balrog`; see docs/importing-balrog.md.\n")
	if r := draftReason(e); r != "" {
		p("# Draft: %s\n", strings.ReplaceAll(r, "\n", " "))
	}
	p("id: %s\nversion: %s\n", id, taskVersion)
	p("title: %s\n", q(fmt.Sprintf("BALROG %s %s seed %d", e.Env, e.Task, e.Seed)))
	p("project: %s\nkind: feature\ninstruction: instruction.md\nrepo_dir: repo\n", q("balrog-"+e.Env))
	p("verifier:\n  tests: verifier/tests\n  required: []\n")
	p("protected: []\n")
	mutants := "[]"
	if len(mutant) > 0 && mutant[0] {
		mutants = "[controls/mutant1.sh]"
	}
	p("controls:\n  reference: controls/reference.sh\n  empty: controls/empty.sh\n  mutants: %s\n  exploit: controls/exploit.sh\n", mutants)
	p("timeouts:\n  agent_s: %d\n", agentTimeout[e.Env])
	p("tags: [balrog, %s]\n", e.Env)
	p("capabilities: [tool_use, planning]\n")
	p("provenance:\n  source: %s\n", q(upstreamRepo))
	if w.up.revision != "" {
		p("  base_commit: %s\n", w.up.revision)
	}
	p("  license: %s\n  imported_from: %s\n  created_by: %s\n",
		q(w.up.license), q(fmt.Sprintf("balrog %s %s seed %d", e.Env, e.Task, e.Seed)), q("bifrost-bench import balrog"))
	p("environment:\n  image: %s\n  verifier_image: %s\n", q(baseTag()), q(w.imageTag))
	p("services:\n")
	p("  - id: %s\n    role: mcp\n    image: %s\n", serviceID, q(w.imageTag))
	p("    env:\n      BALROG_ENV: %s\n      BALROG_TASK: %s\n      BALROG_SEED: %s\n", q(e.Env), q(e.Task), q(fmt.Sprint(e.Seed)))
	p("    port: %d\n", servicePort)
	p("    ready:\n      http: /healthz\n      timeout_s: 180\n")
	p("    mcp:\n      transport: http\n      path: /mcp\n")
	p("    export:\n      - id: %s\n        path: %s\n        max_bytes: %d\n", stateExportID, stateExportPath, stateExportMax)
	p("output:\n  kind: state\n")
	return b.String()
}

func (w *writer) writeSuite(id string, tasks []string, rec schemas.ImportRecord) error {
	prev, err := existingSuite(w.suiteDir)
	if err != nil {
		return err
	}
	all := union(prev, tasks)
	if err := writeFile(filepath.Join(w.suiteDir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return err
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Imported from BALROG by `bifrost-bench import balrog`; see docs/importing-balrog.md.\n")
	p("id: %s\nversion: %s\n", id, suiteVersion)
	p("description: %s\n", q("BALROG: game episodes (BabyAI, Baba Is AI, TextWorld, Crafter, NetHack, MiniHack) played through an MCP game service; the reward is upstream's progression metric (0 to 1), a pass is progression 1."))
	p("environment: docker_workspace\ngrader: verifier_command\n")
	p("base_image:\n  dockerfile: base/Dockerfile\n  tag: %s\n", q(baseTag()))
	p("workdir: /workspace\n")
	p("verifier:\n  command: [python3, -I, /opt/heimdall/verify.py]\n")
	p("  restore: []\n  timeout_s: %d\n  result: reward\n  result_path: /grader/out/reward.json\n  pass_threshold: 1\n", verifierTimeout)
	p("tasks: %s\n", flowList(all))
	p("track: %s\ngrading: %s\n", schemas.TrackMCP, schemas.GradingVerified)
	p("output:\n  kind: state\n")
	p("imported:\n  importer: %s\n  benchmark: %s\n  source: %s\n", rec.Importer, q(rec.Benchmark), q(rec.Source))
	if rec.Revision != "" {
		p("  revision: %s\n", rec.Revision)
	}
	p("  license: %s\n  at: %s\n", q(rec.License), rec.At.Format("2006-01-02T15:04:05Z"))
	return writeFile(filepath.Join(w.suiteDir, "suite.yaml"), []byte(b.String()), 0o644)
}

func instruction(e episode) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# BALROG %s: %s\n\n", e.Env, e.Task)
	b.WriteString("## Game\n\n" + strings.TrimSpace(dedent(e.Instruction)) + "\n\n")
	b.WriteString("## How to play\n\n")
	b.WriteString("- The game runs in the MCP server `" + serviceID + "`. Call `observe` to see the current observation and the valid actions, and `act` with exactly one action to take a step; each `act` returns the next observation.\n")
	fmt.Fprintf(&b, "- The episode allows at most %d steps. An action that is not valid becomes the game's default action.\n", e.MaxSteps)
	b.WriteString("- Your score is the game's progress when you stop (1 means the goal is reached). Nothing in the workspace is graded.\n")
	return b.String()
}

func requirements(e episode) string {
	var b strings.Builder
	b.WriteString("# Requirements\n\n")
	fmt.Fprintf(&b, "The verifier reads upstream BALROG's progression for %s %s, seed %d, from the exported episode and replays the actions as a cross-check.\n\n", e.Env, e.Task, e.Seed)
	b.WriteString("| Check | What it judges |\n|---|---|\n")
	b.WriteString("| `progression` (the reward) | " + progressNote(e.Env) + " A pass is 1. |\n")
	if r := draftReason(e); r != "" {
		b.WriteString("\nDraft: " + r + "\n")
	}
	return b.String()
}

func progressNote(env string) string {
	switch env {
	case "babyai", "babaisai":
		return "1 when the mission is completed, else 0."
	case "textworld":
		return "score / max score at the end, 1 on a win."
	case "crafter":
		return "achievements unlocked / 22."
	case "nle":
		return "upstream's NetHack progress (deepest dungeon level or experience level, mapped to human-play percentiles)."
	default:
		return "1 when the level is solved, else 0."
	}
}

// dedent strips the common leading indentation of upstream's prompts.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	minIndent := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent <= 0 {
		return s
	}
	for i, l := range lines {
		if len(l) >= minIndent {
			lines[i] = l[minIndent:]
		} else {
			lines[i] = strings.TrimLeft(l, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

func controlShell(purpose string, e episode, actions []string) (string, error) {
	if actions == nil {
		actions = []string{}
	}
	doc, err := json.Marshal(map[string]any{"server": serviceID, "actions": actions})
	if err != nil {
		return "", err
	}
	if strings.Contains(controlScript, "\nPY\n") {
		return "", fmt.Errorf("control client holds the here-document marker")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&b, "# %s for BALROG %s %s seed %d.\n# Runs in the agent's place with the services up.\n", purpose, e.Env, e.Task, e.Seed)
	b.WriteString("set -eu\nclient=$(mktemp)\ntrap 'rm -f \"$client\"' EXIT\ncat > \"$client\" <<'PY'\n")
	b.WriteString(controlScript)
	if !strings.HasSuffix(controlScript, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("PY\npython3 -I \"$client\" <<'JSON'\n")
	b.Write(doc)
	b.WriteString("\nJSON\n")
	return b.String(), nil
}

const exploitShell = `#!/bin/sh
# Exploit control: takes no action and forges a winning state file in the
# workspace. The verifier reads only the service's exported state, so it fails.
set -eu
mkdir -p state/balrog
printf '{"format": "heimdall-balrog/1", "progression": 1.0, "done": true, "actions": []}\n' > state/balrog/run
printf 'Done: progression 1.\n' > answer.txt
`

func baseTag() string {
	h := sha256.Sum256([]byte(baseDockerfile))
	return "bench/balrog-base:" + hex.EncodeToString(h[:])[:12]
}

func existingSuite(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s schemas.Suite
	if err := yamlUnmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read existing suite.yaml: %w", err)
	}
	return s.TaskIDs, nil
}

func union(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range append(append([]string(nil), a...), b...) {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func flowList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = q(s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func q(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeFile(p string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, mode)
}

func yamlUnmarshal(b []byte, v any) error { return yaml.Unmarshal(b, v) }
