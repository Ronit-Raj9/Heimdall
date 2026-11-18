package export

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// HarborSchemaVersion is the task.toml schema_version written, the default
// docs.harborframework.com/tasks/configuration names (checked 18 Nov 2026).
const HarborSchemaVersion = "1.4"

// GraderDir is where an exported test.sh lays out what Heimdall's verifier
// container has under /grader (hidden tests, out, artifacts). Every
// "/grader" in the suite verifier command and in the hidden test files is
// rewritten to it, because /grader is Heimdall's own mount point.
const GraderDir = "/tmp/hd-grader"

// Shapes the Harbor export can write: what the verifier reads.
const (
	ShapeDiff     = "diff"     // the workspace, graded like a clean-room diff grade
	ShapeSnapshot = "snapshot" // the agent's container as it was left
	ShapeArtifact = "artifact" // files the agent left in the workspace
	ShapeAnswer   = "answer"   // an answer file the agent wrote
)

// Graders the export can turn into a test.sh.
const (
	graderHiddenTests     = "hidden_tests"
	graderVerifierCommand = "verifier_command"
	graderAnswerMatch     = "answer_match"
)

// verifierMarginS is added to the suite verifier timeout for Harbor's
// verifier timeout, which also covers copying and checking.
const verifierMarginS = 60

// maxRewriteBytes caps the hidden test files whose "/grader" paths are
// rewritten; larger files are copied unchanged.
const maxRewriteBytes = 1 << 20

//go:embed assets/check.py
var checkPy []byte

// ErrHiddenTests refuses an export that would publish hidden tests, expected
// answers and reference solutions without the caller asking for it.
var ErrHiddenTests = errors.New("a Harbor task carries its verifier: exporting publishes the hidden tests, expected answers and reference solutions; pass -include-hidden to do it on purpose")

// HarborOptions selects and shapes a Harbor export.
type HarborOptions struct {
	// Tasks are the task ids to export; empty exports every public task.
	Tasks []string
	// IncludeHidden writes the hidden tests, the expected answer and the
	// reference solution, which a Harbor task needs to be graded. Without
	// it, and without Withhold, the export is refused (ErrHiddenTests).
	IncludeHidden bool
	// Withhold writes each task with its verifier and solution left out:
	// tests/test.sh exits 3 without a reward. The API serves only this.
	Withhold bool
	// RequireAdmitted skips tasks the suite lock does not mark admitted.
	RequireAdmitted bool
	// Exporter is written into task.toml metadata (program and version).
	Exporter string
}

// HarborTask is one task the export wrote.
type HarborTask struct {
	ID       string   `json:"id"`
	Folder   string   `json:"folder"`
	Name     string   `json:"name"`  // task.toml [task] name
	Shape    string   `json:"shape"` // diff | snapshot | artifact | answer
	Withheld bool     `json:"withheld,omitempty"`
	Notes    []string `json:"notes,omitempty"` // what changed on the way (README.md lists them too)
}

// Skip is a task left out, with the reason.
type Skip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// HarborResult says what an export wrote and left out.
type HarborResult struct {
	Suite    string       `json:"suite"` // id@version
	Tasks    []HarborTask `json:"tasks"`
	Skipped  []Skip       `json:"skipped"`
	Warnings []string     `json:"warnings,omitempty"`
}

// file is one file of an exported task.
type file struct {
	data []byte
	mode os.FileMode
}

// harborTree is one task folder: slash paths relative to it.
type harborTree map[string]file

// PlanHarbor converts the suite's selected tasks into Harbor task folders in
// memory, without writing. It returns the folders by task folder name in
// result order. A task the format cannot express honestly is skipped with
// the reason; held-out tasks are always skipped.
func PlanHarbor(s *schemas.Suite, opts HarborOptions) (map[string]harborTree, *HarborResult, error) {
	if s == nil {
		return nil, nil, errors.New("export harbor: no suite")
	}
	if !opts.IncludeHidden && !opts.Withhold {
		return nil, nil, ErrHiddenTests
	}
	res := &HarborResult{Suite: s.ID + "@" + s.Version, Tasks: []HarborTask{}, Skipped: []Skip{}}
	if opts.IncludeHidden {
		res.Warnings = append(res.Warnings, "the export holds the hidden tests, expected answers and reference solutions: anyone who can read it can pass these tasks without solving them; do not publish it where agents under test can read it")
	}
	byID := map[string]*schemas.Task{}
	for _, t := range s.Tasks {
		byID[t.ID] = t
	}
	var picked []*schemas.Task
	if len(opts.Tasks) == 0 {
		picked = append(picked, s.Tasks...)
	} else {
		for _, id := range opts.Tasks {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			t, ok := byID[id]
			if !ok {
				res.Skipped = append(res.Skipped, Skip{ID: id, Reason: "no task with this id in the suite"})
				continue
			}
			picked = append(picked, t)
		}
	}
	trees := map[string]harborTree{}
	for _, t := range picked {
		if t.Split == schemas.SplitHeldOut {
			res.Skipped = append(res.Skipped, Skip{ID: t.ID, Reason: "held-out task: never exported, so it stays unseen"})
			continue
		}
		tree, task, reason, err := planTask(s, t, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("export harbor: task %s: %w", t.ID, err)
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, Skip{ID: t.ID, Reason: reason})
			continue
		}
		trees[task.Folder] = tree
		res.Tasks = append(res.Tasks, *task)
	}
	return trees, res, nil
}

// shapeOf returns the task's export shape and grading facts, or a reason it
// cannot be exported.
func shapeOf(s *schemas.Suite, t *schemas.Task) (string, string) {
	kind := schemas.OutputDiff
	if s.Output != nil && s.Output.Kind != "" {
		kind = s.Output.Kind
	}
	if t.Output != nil && t.Output.Kind != "" {
		kind = t.Output.Kind
	}
	switch {
	case s.Grading == schemas.GradingJudged:
		return "", "judged suite: the grade comes from a pinned judge model through the gateway, which a Harbor test.sh cannot call without a key; not exported"
	case len(s.Services) > 0 || len(t.Services) > 0:
		return "", "needs services beside the agent (databases, tool servers, simulated users); a single Harbor container cannot hold them as the bench isolates them, and no docker-compose export is written"
	case len(t.Sessions) > 1:
		return "", "multi-session task: each session is a fresh agent process with only its memory carried over; Harbor's multi-step tasks share one environment, which is not the same test"
	case len(s.External) > 0 || len(t.External) > 0:
		return "", "calls external HTTP APIs through the bench's recording tool proxy; a Harbor container would reach them directly, unrecorded"
	case (s.Imported != nil && s.Imported.Importer == "harbor") || (t.Provenance != nil && t.Provenance.CreatedBy == "bifrost-bench import harbor"):
		return "", "imported from Harbor: the upstream task folder is the Harbor task; wrapping the bench's Harbor wrapper again would nest two verifiers"
	case t.Impossible:
		return "", "impossible task: a pass can only come from cheating, which the bench catches with its impossible_pass gate; Harbor has no such gate"
	case kind == schemas.OutputState:
		return "", "graded from service state exported after the run; needs services"
	}
	switch kind {
	case schemas.OutputAnswer:
		if s.Grader != graderAnswerMatch {
			return "", fmt.Sprintf("answer task graded by %q; only answer_match is exported", s.Grader)
		}
		return ShapeAnswer, ""
	case schemas.OutputArtifact:
		if s.Grader != graderHiddenTests && s.Grader != graderVerifierCommand {
			return "", fmt.Sprintf("artifact task graded by %q; only hidden_tests and verifier_command are exported", s.Grader)
		}
		return ShapeArtifact, ""
	case schemas.OutputDiff:
		if s.Grader != graderHiddenTests && s.Grader != graderVerifierCommand {
			return "", fmt.Sprintf("graded by %q; only hidden_tests and verifier_command are exported", s.Grader)
		}
		if s.Verifier.Mode == schemas.VerifierSnapshot {
			return ShapeSnapshot, ""
		}
		return ShapeDiff, ""
	}
	return "", fmt.Sprintf("output kind %q is not exported", kind)
}

// taskName is the task.toml [task] name, org/name as Harbor requires.
func taskName(s *schemas.Suite, t *schemas.Task) string {
	return "heimdall/" + s.ID + "-" + t.ID
}

// planTask builds one task folder.
func planTask(s *schemas.Suite, t *schemas.Task, opts HarborOptions) (harborTree, *HarborTask, string, error) {
	shape, reason := shapeOf(s, t)
	if reason != "" {
		return nil, nil, reason, nil
	}
	status := ""
	if s.Lock != nil {
		status = string(s.Lock.Tasks[t.ID].Status)
	}
	if opts.RequireAdmitted && status != string(schemas.TaskAdmitted) {
		return nil, nil, fmt.Sprintf("not admitted in the suite lock (status %q); only admitted tasks are exported here", status), nil
	}
	if len(s.Verifier.Command) == 0 && shape != ShapeAnswer {
		return nil, nil, "the suite has no verifier command", nil
	}
	task := &HarborTask{ID: t.ID, Folder: t.ID, Name: taskName(s, t), Shape: shape, Withheld: opts.Withhold && !opts.IncludeHidden}
	tree := harborTree{}
	instr, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(t.Instruction)))
	if err != nil {
		return nil, nil, "", fmt.Errorf("read instruction: %w", err)
	}
	tree["instruction.md"] = file{data: instr, mode: 0o644}

	env, err := planEnvironment(s, t, tree)
	if err != nil {
		var skip *skipError
		if errors.As(err, &skip) {
			return nil, nil, skip.reason, nil
		}
		return nil, nil, "", err
	}
	task.Notes = append(task.Notes, env.notes...)
	if !task.Withheld {
		notes, reason, err := planSolution(s, t, shape, env, tree)
		if err != nil {
			return nil, nil, "", err
		}
		if reason != "" {
			return nil, nil, reason, nil
		}
		task.Notes = append(task.Notes, notes...)
		notes, reason, err = planTests(s, t, shape, env, tree)
		if err != nil {
			return nil, nil, "", err
		}
		if reason != "" {
			return nil, nil, reason, nil
		}
		task.Notes = append(task.Notes, notes...)
	} else {
		tree["tests/test.sh"] = file{data: []byte(withheldTestSh), mode: 0o755}
		task.Notes = append(task.Notes, "verifier and reference solution withheld: tests/test.sh exits 3 without a reward; export with the CLI and -include-hidden to get them")
	}
	tree["task.toml"] = file{data: []byte(taskTOML(s, t, shape, status, env, opts, task.Withheld)), mode: 0o644}
	tree["README.md"] = file{data: []byte(taskReadme(s, t, task)), mode: 0o644}
	return tree, task, "", nil
}

const withheldTestSh = `#!/bin/bash
# The verifier of this task was withheld from this export (hidden tests are
# never served by the API). Export with the CLI and -include-hidden to get it.
echo "verifier withheld from this export; no reward written" >&2
exit 3
`

// skipError is a reason a task cannot be exported, found while planning.
type skipError struct{ reason string }

func (e *skipError) Error() string { return e.reason }

// envPlan is what the environment decided: the workspace path, the image's
// final user, and notes.
type envPlan struct {
	workdir     string
	user        string // the image's final user; "" or root means root
	dockerImage string // task.toml docker_image when the task names a prebuilt image
	repoSeeded  bool   // the workspace comes from the task's repo folder
	notes       []string
}

var userRE = regexp.MustCompile(`(?im)^\s*USER\s+(\S+)\s*$`)
var fromRE = regexp.MustCompile(`(?im)^\s*FROM\s+`)

// lastUser returns the user the Dockerfile ends with, "" for root.
func lastUser(dockerfile string) string {
	m := userRE.FindAllStringSubmatch(dockerfile, -1)
	if len(m) == 0 {
		return ""
	}
	u := m[len(m)-1][1]
	if u == "root" || u == "0" || u == "0:0" {
		return ""
	}
	return u
}

// planEnvironment writes environment/: the task's own image or the suite's
// base image, and the task repository as the workspace with a git baseline.
func planEnvironment(s *schemas.Suite, t *schemas.Task, tree harborTree) (*envPlan, error) {
	p := &envPlan{workdir: s.Workdir}
	if p.workdir == "" {
		p.workdir = "/workspace"
	}
	var dockerfile string
	switch {
	case t.Environment != nil && t.Environment.Dockerfile != "":
		df := filepath.Join(t.Dir, filepath.FromSlash(t.Environment.Dockerfile))
		ctx := filepath.Dir(df)
		if t.Environment.Context != "" {
			ctx = filepath.Join(t.Dir, filepath.FromSlash(t.Environment.Context))
		}
		if err := copyTree(tree, "environment", ctx, nil); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(df)
		if err != nil {
			return nil, fmt.Errorf("read task Dockerfile: %w", err)
		}
		dockerfile = string(b)
		p.notes = append(p.notes, "environment: the task's own Dockerfile and build context")
	case t.Environment != nil && t.Environment.Image != "":
		p.dockerImage = t.Environment.Image
		dockerfile = "FROM " + t.Environment.Image + "\n"
		p.notes = append(p.notes, "environment: the task's prebuilt image "+t.Environment.Image)
	case s.BaseImage.Dockerfile != "":
		df := filepath.Join(s.Root, filepath.FromSlash(s.BaseImage.Dockerfile))
		if err := copyTree(tree, "environment", filepath.Dir(df), nil); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(df)
		if err != nil {
			return nil, fmt.Errorf("read suite base Dockerfile: %w", err)
		}
		dockerfile = string(b)
		p.notes = append(p.notes, "environment: the suite base image ("+s.BaseImage.Dockerfile+") plus the task repository")
	case s.BaseImage.Tag != "":
		ref := s.BaseImage.Tag
		if s.Lock != nil && s.Lock.BaseImage.Digest != "" && !strings.Contains(ref, "@") {
			ref += "@" + s.Lock.BaseImage.Digest
		}
		dockerfile = "FROM " + ref + "\n"
		p.notes = append(p.notes, "environment: the suite base image "+ref+" (prebuilt; it must be pullable where Harbor runs)")
	default:
		return nil, &skipError{"the suite names no base image and the task no image of its own"}
	}
	if !fromRE.MatchString(dockerfile) {
		return nil, &skipError{"the Dockerfile has no FROM line"}
	}
	if t.Environment != nil && t.Environment.WorkspacePath != "" {
		p.workdir = t.Environment.WorkspacePath
	} else {
		p.repoSeeded = true
	}
	p.user = lastUser(dockerfile)
	if t.Environment != nil && t.Environment.User == "root" {
		p.user = ""
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(dockerfile, "\n"))
	b.WriteString("\n\n# Task workspace.\nUSER root\n")
	if p.repoSeeded {
		if t.RepoDir == "" {
			return nil, &skipError{"the workspace comes from the task's repo folder, but the task names none"}
		}
		if err := copyTree(tree, "environment/repo", filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir)), []string{".git"}); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "COPY repo/ %s/\n", p.workdir)
		// The bench diffs the workspace against a git baseline; agents expect one too.
		fmt.Fprintf(&b, "RUN mkdir -p %[1]s && cd %[1]s && if command -v git >/dev/null 2>&1; then git -c safe.directory='*' init -q && git -c safe.directory='*' add -A && git -c safe.directory='*' -c user.name=task -c user.email=task@localhost commit -qm baseline >/dev/null; fi\n", p.workdir)
		if p.user != "" {
			fmt.Fprintf(&b, "RUN chown -R %s %s\n", p.user, p.workdir)
		}
	}
	if p.user != "" {
		fmt.Fprintf(&b, "USER %s\n", p.user)
	}
	fmt.Fprintf(&b, "WORKDIR %s\n", p.workdir)
	tree["environment/Dockerfile"] = file{data: []byte(b.String()), mode: 0o644}
	return p, nil
}

// planSolution writes solution/solve.sh from the task's reference control.
func planSolution(s *schemas.Suite, t *schemas.Task, shape string, env *envPlan, tree harborTree) ([]string, string, error) {
	ref := t.Controls.Reference
	if ref == "" {
		return nil, "the task has no reference control, so solution/solve.sh would be empty", nil
	}
	data, err := os.ReadFile(filepath.Join(t.Dir, filepath.FromSlash(ref)))
	if err != nil {
		return nil, "", fmt.Errorf("read reference control: %w", err)
	}
	head := "#!/bin/bash\n# Reference solution exported from Heimdall (" + ref + ").\nset -euo pipefail\nSOL=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\ncd " + shq(env.workdir) + "\n"
	var solve string
	switch {
	case shape == ShapeAnswer:
		tree["solution/answer.txt"] = file{data: data, mode: 0o644}
		solve = head + "cp \"$SOL/answer.txt\" " + shq(answerPath(s, t)) + "\n"
	case strings.HasSuffix(ref, ".diff") || strings.HasSuffix(ref, ".patch"):
		tree["solution/patch.diff"] = file{data: data, mode: 0o644}
		solve = head + "if command -v git >/dev/null 2>&1; then\n\tgit -c safe.directory='*' apply --whitespace=nowarn \"$SOL/patch.diff\"\nelse\n\tpatch -p1 < \"$SOL/patch.diff\"\nfi\n"
	case strings.HasSuffix(ref, ".sh"):
		tree["solution/reference.sh"] = file{data: data, mode: 0o755}
		solve = head + "sh \"$SOL/reference.sh\"\n"
	default:
		return nil, fmt.Sprintf("reference control %s is neither a patch nor a script", ref), nil
	}
	tree["solution/solve.sh"] = file{data: []byte(solve), mode: 0o755}
	return nil, "", nil
}

// answerPath is where an answer task's agent writes its answer.
func answerPath(s *schemas.Suite, t *schemas.Task) string {
	p := ""
	if s.Output != nil {
		p = s.Output.AnswerPath
	}
	if t.Output != nil && t.Output.AnswerPath != "" {
		p = t.Output.AnswerPath
	}
	if p == "" {
		p = "answer.txt"
	}
	return p
}

// planTests writes tests/: test.sh, the hidden tests under heimdall/hidden,
// the pristine restore paths, the checker and its inputs.
func planTests(s *schemas.Suite, t *schemas.Task, shape string, env *envPlan, tree harborTree) ([]string, string, error) {
	var notes []string
	tree["tests/heimdall/check.py"] = file{data: checkPy, mode: 0o644}
	var b strings.Builder
	b.WriteString("#!/bin/bash\n")
	fmt.Fprintf(&b, "# Verifier exported from Heimdall suite %s@%s, task %s@%s (grader %s).\n", s.ID, s.Version, t.ID, t.Version, s.Grader)
	b.WriteString("# Runs inside the agent's container after the agent (Harbor's shared verifier);\n# README.md says how this differs from the bench's own grade.\n")
	b.WriteString("set -u\nHERE=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n")
	fmt.Fprintf(&b, "G=%s\nWS=%s\n", GraderDir, shq(env.workdir))
	b.WriteString("mkdir -p /logs/verifier\n")
	b.WriteString("command -v python3 >/dev/null 2>&1 || { echo \"python3 is needed to read the verifier result\" >&2; exit 3; }\n")
	if shape == ShapeAnswer {
		spec, err := answerSpec(t)
		if err != nil {
			return nil, "", err
		}
		if spec == nil {
			return nil, "answer task without an expected answer file", nil
		}
		if spec.Abstain || !slices.Contains([]string{"exact", "contains_all", "regex", "numeric", "choice"}, spec.Match) {
			return nil, fmt.Sprintf("answer match %q (abstain %v) is not exported; only exact, contains_all, regex, numeric and choice are checked the same way outside the bench", spec.Match, spec.Abstain), nil
		}
		js, err := json.MarshalIndent(map[string]any{"match": spec.Match, "expected": spec.Expected, "normalize": spec.Normalize, "tolerance": spec.Tolerance}, "", "  ")
		if err != nil {
			return nil, "", err
		}
		tree["tests/heimdall/answer.json"] = file{data: js, mode: 0o644}
		fmt.Fprintf(&b, "cd \"$WS\"\npython3 \"$HERE/heimdall/check.py\" answer %s 0 \"$HERE/heimdall/answer.json\" > \"/logs/verifier/reward.txt.tmp\" || exit 3\nmv /logs/verifier/reward.txt.tmp /logs/verifier/reward.txt\n", shq(answerPath(s, t)))
		notes = append(notes, "answer: read only from the answer file; the bench falls back to the agent's final message, which Harbor does not hand to the verifier")
		tree["tests/test.sh"] = file{data: []byte(b.String()), mode: 0o755}
		return notes, "", nil
	}
	if t.Verifier.Tests == "" {
		return nil, "no hidden tests folder", nil
	}
	rewritten, err := copyHidden(tree, filepath.Join(t.Dir, filepath.FromSlash(t.Verifier.Tests)))
	if err != nil {
		var skip *skipError
		if errors.As(err, &skip) {
			return nil, skip.reason, nil
		}
		return nil, "", err
	}
	if rewritten > 0 {
		notes = append(notes, fmt.Sprintf("%d hidden test file(s): /grader paths rewritten to %s", rewritten, GraderDir))
	}
	b.WriteString("rm -rf \"$G\" && mkdir -p \"$G/tests\" \"$G/out\" || exit 3\ncp -a \"$HERE/heimdall/hidden/.\" \"$G/tests/\" || exit 3\n")
	if shape == ShapeDiff && len(s.Verifier.Restore) > 0 && !env.repoSeeded {
		notes = append(notes, "diff: graded in place; the restore paths are not reset, because the pristine workspace lives in the task image, not in the export")
	}
	if shape == ShapeDiff && len(s.Verifier.Restore) > 0 && env.repoSeeded {
		repo := filepath.Join(t.Dir, filepath.FromSlash(t.RepoDir))
		for _, p := range s.Verifier.Restore {
			src := filepath.Join(repo, filepath.FromSlash(p))
			if fi, err := os.Lstat(src); err == nil {
				if fi.IsDir() {
					if err := copyTree(tree, "tests/heimdall/restore/"+p, src, nil); err != nil {
						return nil, "", err
					}
				} else if fi.Mode().IsRegular() {
					data, err := os.ReadFile(src)
					if err != nil {
						return nil, "", err
					}
					tree["tests/heimdall/restore/"+p] = file{data: data, mode: fi.Mode().Perm()}
				}
			}
		}
		b.WriteString("# Paths the agent may not change are reset from the pristine repository\n# (the suite's verifier.restore list), as the bench's clean room does.\n")
		fmt.Fprintf(&b, "for p in %s; do\n", shWords(s.Verifier.Restore))
		b.WriteString("\trm -rf \"$WS/$p\"\n\tif [ -e \"$HERE/heimdall/restore/$p\" ]; then mkdir -p \"$(dirname \"$WS/$p\")\" && cp -a \"$HERE/heimdall/restore/$p\" \"$WS/$p\"; fi\ndone\n")
		notes = append(notes, "diff: graded in place in the agent's workspace with the restore paths reset, not by applying an exported diff to a fresh copy")
	}
	if shape == ShapeArtifact {
		var arts []schemas.ArtifactSpec
		if s.Output != nil {
			arts = s.Output.Artifacts
		}
		if t.Output != nil && len(t.Output.Artifacts) > 0 {
			arts = t.Output.Artifacts
		}
		b.WriteString("# Artifacts the agent left, copied where the verifier reads them.\n")
		notes = append(notes, "artifact: matches are copied without the bench's size cap, and a missing required artifact is left to the verifier to fail (the bench fails the grade itself)")
		for _, a := range arts {
			if strings.ContainsAny(a.Path, "\"`$\\\n") || strings.HasPrefix(a.Path, "/") || strings.Contains(a.Path, "..") {
				return nil, fmt.Sprintf("artifact %s path %q cannot be written safely into a shell glob", a.ID, a.Path), nil
			}
			fmt.Fprintf(&b, "mkdir -p \"$G/artifacts/%s\"\nfor f in \"$WS\"/%s; do if [ -f \"$f\" ] && [ ! -L \"$f\" ]; then cp \"$f\" \"$G/artifacts/%s/\"; fi; done\n", a.ID, globQuote(a.Path), a.ID)
		}
	}
	timeout := s.Verifier.TimeoutS
	if timeout <= 0 {
		timeout = 300
	}
	b.WriteString("cd \"$WS\" || exit 3\n")
	fmt.Fprintf(&b, "if command -v timeout >/dev/null 2>&1; then T=\"timeout -k 10 %d\"; else T=\"\"; fi\n", timeout)
	cmd := make([]string, len(s.Verifier.Command))
	for i, a := range s.Verifier.Command {
		cmd[i] = shq(rewriteGrader(a))
	}
	b.WriteString("$T " + strings.Join(cmd, " ") + "\ncode=$?\n")
	result := s.Verifier.Result
	if result == "" {
		result = schemas.VerifierResultJUnit
	}
	resPath := s.Verifier.ResultPath
	extra := ""
	switch result {
	case schemas.VerifierResultJUnit:
		if resPath == "" {
			resPath = "/grader/out/junit.xml"
		}
		if len(t.Verifier.Required) > 0 {
			tree["tests/heimdall/required.txt"] = file{data: []byte(strings.Join(t.Verifier.Required, "\n") + "\n"), mode: 0o644}
			extra = "\"$HERE/heimdall/required.txt\""
		}
	case schemas.VerifierResultReward:
		if resPath == "" {
			resPath = "/grader/out/reward.json"
		}
		th := s.Verifier.PassThreshold
		if th == 0 {
			th = 1
		}
		extra = fmt.Sprint(th)
		notes = append(notes, fmt.Sprintf("reward: Harbor gets the reward itself; the bench passes it at %v", th))
	case schemas.VerifierResultExitCode:
		resPath = "-"
	default:
		return nil, fmt.Sprintf("verifier result %q is not exported", result), nil
	}
	fmt.Fprintf(&b, "python3 \"$HERE/heimdall/check.py\" %s %s \"$code\" %s > \"$G/reward\" || exit 3\n", result, shq(rewriteGrader(resPath)), extra)
	b.WriteString("cp \"$G/reward\" /logs/verifier/reward.txt\n")
	tree["tests/test.sh"] = file{data: []byte(b.String()), mode: 0o755}
	if shape == ShapeSnapshot {
		notes = append(notes, "snapshot: graded in the agent's live container, as the bench's snapshot verifier grades a frozen copy of it")
	}
	return notes, "", nil
}

// answerSpec reads the task's hidden expected answer.
func answerSpec(t *schemas.Task) (*schemas.AnswerSpec, error) {
	rel := t.Verifier.Answer
	if rel == "" {
		rel = "verifier/answer.yaml"
	}
	p := filepath.Join(t.Dir, filepath.FromSlash(rel))
	if _, err := os.Stat(p); err != nil {
		return nil, nil
	}
	var spec schemas.AnswerSpec
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read expected answer: %w", err)
	}
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("read expected answer: %w", err)
	}
	return &spec, nil
}

var graderRE = regexp.MustCompile(`/grader(/|$|["' =:])`)

// rewriteGrader moves a /grader path to GraderDir.
func rewriteGrader(s string) string {
	return graderRE.ReplaceAllString(s, GraderDir+"$1")
}

// copyHidden copies the hidden tests under tests/heimdall/hidden, rewriting
// /grader paths in text files, and returns how many files it rewrote.
func copyHidden(tree harborTree, dir string) (int, error) {
	n := 0
	err := walkFiles(dir, nil, func(rel string, data []byte, mode os.FileMode) error {
		if len(data) <= maxRewriteBytes && utf8.Valid(data) && graderRE.Match(data) {
			data = []byte(rewriteGrader(string(data)))
			n++
		}
		tree["tests/heimdall/hidden/"+rel] = file{data: data, mode: mode}
		return nil
	})
	return n, err
}

// copyTree copies the regular files under dir to prefix/ in the tree,
// leaving out the named top-level entries. A symlink makes the task a skip.
func copyTree(tree harborTree, prefix, dir string, exclude []string) error {
	return walkFiles(dir, exclude, func(rel string, data []byte, mode os.FileMode) error {
		tree[prefix+"/"+rel] = file{data: data, mode: mode}
		return nil
	})
}

// walkFiles calls fn for every regular file under dir in path order.
func walkFiles(dir string, exclude []string, fn func(rel string, data []byte, mode os.FileMode) error) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if slices.Contains(exclude, strings.SplitN(rel, "/", 2)[0]) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return &skipError{"a symlink in the task (" + rel + "); Harbor uploads folders without following links, so it is not exported"}
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		return fn(rel, data, mode)
	})
}

// shq quotes s for a POSIX shell.
func shq(s string) string {
	if s != "" && regexp.MustCompile(`^[A-Za-z0-9_./:=@%+-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shWords quotes each word.
func shWords(ws []string) string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = shq(w)
	}
	return strings.Join(out, " ")
}

// globQuote quotes a workspace-relative glob so only its *, ? and [...]
// expand: each literal run is single-quoted.
func globQuote(p string) string {
	var b strings.Builder
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			b.WriteString("'" + strings.ReplaceAll(lit.String(), "'", `'\''`) + "'")
			lit.Reset()
		}
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch c {
		case '*', '?':
			flush()
			b.WriteByte(c)
		case '[':
			end := strings.IndexByte(p[i:], ']')
			if end < 0 {
				lit.WriteByte(c)
				continue
			}
			flush()
			b.WriteString(p[i : i+end+1])
			i += end
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	return b.String()
}

// sortedPaths returns the tree's paths in order.
func (t harborTree) sortedPaths() []string {
	out := make([]string, 0, len(t))
	for p := range t {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// dirsOf returns every parent folder of the tree's files, sorted.
func (t harborTree) dirsOf() []string {
	seen := map[string]bool{}
	for p := range t {
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
