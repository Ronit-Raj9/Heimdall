package harbor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// Paths inside an imported task folder.
const (
	envDir        = "environment"
	skillsDir     = "skills"
	testsDir      = "verifier/tests"
	upstreamTests = testsDir + "/upstream"
	harborDir     = testsDir + "/harbor"
	solutionDir   = "solution"
	referenceFile = "controls/reference.sh"
	emptyFile     = "controls/empty.sh"
	exploitFile   = "controls/exploit.sh"
	repoDir       = "repo"
)

// writeTask writes one converted task folder:
//
//	task.yaml, instruction.md, IMPORT.md   the task and what the import changed
//	environment/                           upstream build context, Dockerfile rewritten
//	skills/                                the task's skills (SkillsBench)
//	verifier/tests/upstream/               upstream tests, unchanged (/tests at verify)
//	verifier/tests/harbor/                 verify.sh wrapper and the offline test.sh
//	solution/                              upstream solution, unchanged
//	controls/                              reference, empty and exploit scripts
//	repo/                                  empty: the workspace comes from the image
func writeTask(dir, id string, t *upstreamTask, p *taskPlan, up upstream) error {
	for _, d := range []string{repoDir, "controls", harborDir} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
			return err
		}
	}
	skip := map[string]bool{}
	if p.skills {
		skip["skills"] = true
	}
	if err := copyTree(t.EnvDir, filepath.Join(dir, envDir), skip); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, envDir, "Dockerfile"), []byte(p.dockerfile), 0o644); err != nil {
		return err
	}
	if p.skills {
		if err := copyTree(filepath.Join(t.EnvDir, "skills"), filepath.Join(dir, skillsDir), nil); err != nil {
			return err
		}
	}
	if err := copyTree(t.TestsDir, filepath.Join(dir, filepath.FromSlash(upstreamTests)), nil); err != nil {
		return err
	}
	if isDir(t.SolutionDir) {
		if err := copyTree(t.SolutionDir, filepath.Join(dir, solutionDir), nil); err != nil {
			return err
		}
	}
	files := map[string]string{
		"instruction.md":         t.Instruction,
		harborDir + "/test.sh":   p.tests.Script,
		harborDir + "/verify.sh": verifyWrapper(id, t, p.workdir, up, p.separate),
		referenceFile:            referenceScript(t, p),
		emptyFile:                emptyScript,
		exploitFile:              exploitScript,
		"IMPORT.md":              importNotes(id, t, p, up),
	}
	for rel, body := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), mode); err != nil {
			return err
		}
	}
	if p.egressFetch != "" { // recorded internet (egress.go)
		if err := writeEgress(dir); err != nil {
			return err
		}
	}
	rec := taskRecord(id, t, p, up)
	if p.waive {
		ws, err := lintWaivers(dir, t)
		if err != nil {
			return err
		}
		rec.LintWaivers = ws
		if len(ws) > 0 {
			if err := appendFile(filepath.Join(dir, "IMPORT.md"), waiverNotes(ws)); err != nil {
				return err
			}
		}
	}
	return writeYAML(filepath.Join(dir, "task.yaml"), rec)
}

// waiverReason is the reason every import waiver gives.
const waiverReason = "verbatim upstream instruction, no test content"

// lintWaivers (-set lint_waivers=upstream) are the waivers of a library task
// whose upstream text trips the two heuristic checks: each leak word of the
// verbatim instruction, and each high-entropy *_KEY/*_TOKEN finding in the
// written task folder. Real secret patterns are never waived.
func lintWaivers(dir string, t *upstreamTask) ([]schemas.LintWaiver, error) {
	var out []schemas.LintWaiver
	for _, term := range suite.InstructionLeakTerms(t.Instruction) {
		out = append(out, schemas.LintWaiver{Rule: schemas.LintRuleInstructionTerm, Path: "instruction.md", Match: term, Reason: waiverReason})
	}
	found, err := suite.ScanSecrets(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if strings.HasPrefix(f.Kind, "high-entropy ") {
			out = append(out, schemas.LintWaiver{Rule: schemas.LintRuleHighEntropy, Path: f.Path, Line: f.Line, Match: f.Masked,
				Reason: "verbatim upstream file, an identifier and not a secret; no test content"})
		}
	}
	return out, nil
}

// waiverNotes is the IMPORT.md section listing the waivers.
func waiverNotes(ws []schemas.LintWaiver) string {
	var b strings.Builder
	b.WriteString("\n## Lint waivers\n\nRecorded in `task.yaml` (`lint_waivers`, -set lint_waivers=upstream); each turns one static-validation error into a warning:\n\n")
	for _, w := range ws {
		where := w.Path
		if w.Line > 0 {
			where = fmt.Sprintf("%s line %d", w.Path, w.Line)
		}
		fmt.Fprintf(&b, "- `%s` in %s, matched `%s`: %s.\n", w.Rule, where, w.Match, w.Reason)
	}
	return b.String()
}

func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// taskRecord is the task.yaml of a converted task.
func taskRecord(id string, t *upstreamTask, p *taskPlan, up upstream) *schemas.Task {
	task := &schemas.Task{
		ID:          id,
		Version:     taskVersion,
		Title:       title(t),
		Project:     projectName(up.Benchmark),
		Kind:        schemas.TaskFeature,
		Instruction: "instruction.md",
		RepoDir:     repoDir,
		Verifier:    schemas.TaskVerifier{Tests: testsDir, Required: []string{}},
		Protected:   []string{},
		Controls:    schemas.TaskControls{Reference: referenceFile, Empty: emptyFile, Mutants: []string{}, Exploit: exploitFile},
		Timeouts:    schemas.TaskTimeouts{AgentS: p.agentS},
		Tags:        tags(t),
		Provenance: &schemas.TaskProvenance{
			Source:       up.Source,
			License:      up.License,
			ImportedFrom: up.Benchmark,
			CreatedBy:    importerCreatedBy,
		},
		Environment: &schemas.TaskEnvironment{
			Dockerfile:    envDir + "/Dockerfile",
			Context:       envDir,
			WorkspacePath: p.workdir,
			BuildNetwork:  true,
			User:          "root", // Harbor runs the agent, solve.sh and test.sh as root
		},
	}
	if p.separate {
		task.Environment.VerifierImage = t.VerifierImage
		for _, a := range t.Artifacts {
			task.Environment.VerifierArtifacts = append(task.Environment.VerifierArtifacts, a.Source)
		}
		task.Tags = dedupe(append(task.Tags, "separate-verifier"))
	}
	if hexRE.MatchString(up.Revision) {
		task.Provenance.BaseCommit = up.Revision
	}
	switch d := strings.ToLower(t.Difficulty); d {
	case "easy", "medium", "hard":
		task.Difficulty = d
	}
	if p.skills {
		task.Skills = skillsDir
	}
	if p.compose != nil && p.compose.file != "" { // X2: the services beside the agent (compose.go)
		task.Compose = p.compose.file
	}
	if p.egressFetch != "" { // recorded internet (egress.go)
		task.Services = append(task.Services, egressService(p.egressVerifier))
		task.Tags = dedupe(append(task.Tags, EgressTag))
	}
	if p.honeypot {
		task.Protected = []string{"admin/**"}
		task.Tags = dedupe(append(task.Tags, "hack-verifiable"))
	}
	if t.ContinueUntilTimeout {
		task.Tags = dedupe(append(task.Tags, "continue-until-timeout"))
	}
	if p.draft != "" {
		task.Tags = dedupe(append(task.Tags, "draft"))
	}
	return task
}

// title is the upstream description when it is short, else the task id.
func title(t *upstreamTask) string {
	d := strings.Join(strings.Fields(t.Description), " ")
	if d != "" && len(d) <= 120 {
		return d
	}
	return t.ID
}

// projectName is the benchmark without its version.
func projectName(benchmark string) string {
	name, _, _ := strings.Cut(benchmark, "@")
	if name == "" {
		return "harbor"
	}
	return name
}

// tags are the category, an unmapped difficulty, and upstream's tags.
func tags(t *upstreamTask) []string {
	var out []string
	if t.Category != "" {
		out = append(out, "category:"+t.Category)
	}
	switch d := strings.ToLower(t.Difficulty); d {
	case "", "easy", "medium", "hard":
	default:
		out = append(out, "difficulty:"+d)
	}
	return dedupe(append(out, t.Tags...))
}

// verifyWrapper is the suite's verifier command for one task. It recreates
// what Harbor gives test.sh (the tests folder at its mount, an empty
// /logs/verifier, the task's working directory and verifier environment),
// runs the offline test.sh, and copies the reward Harbor would read to
// /grader/out/reward.json. The folders are emptied first, so nothing the
// agent left there can stand in for a reward. On a read-only root file
// system it runs from /tmp instead, with the two paths rewritten in test.sh.
func verifyWrapper(id string, t *upstreamTask, workdir string, up upstream, separate bool) string {
	var env strings.Builder
	if separate {
		// Harbor's separate verifier: the task.toml artifacts arrive from the
		// agent's container at /grader/collect and are uploaded to their own
		// paths in the verifier image, as Harbor uploads them.
		// Existing folders (/, /app, ...) keep their owner and mode: upstream
		// verifiers refuse a /tests whose parent is not root-owned.
		env.WriteString("if [ -d /grader/collect ]; then\n" +
			"\tif tar --version 2>/dev/null | grep -q GNU; then (cd /grader/collect && tar -cf - .) | tar -xf - -C / --no-overwrite-dir || fail \"cannot place the agent's artifacts\"\n" +
			"\telse for e in /grader/collect/* /grader/collect/.[!.]*; do [ -e \"$e\" ] && { cp -a \"$e\" / || fail \"cannot place the agent's artifacts\"; }; done; fi\n" +
			"fi\n")
	}
	for _, k := range sortedEnvKeys(t.VerifierEnv) {
		fmt.Fprintf(&env, "export %s=%s\n", k, shellQuote(t.VerifierEnv[k]))
	}
	return fmt.Sprintf(`#!/bin/sh
# Verifier wrapper for task %[1]s (%[2]s), written by bifrost-bench import harbor.
# It gives the upstream test script what Harbor gives it: the tests folder at
# %[3]s, an empty /logs/verifier and the task's working directory. Then it
# copies the reward Harbor reads (/logs/verifier/reward.txt, or the "reward"
# key of reward.json) to /grader/out/reward.json, where the bench's reward
# grader reads it.
set -u
MOUNT=%[3]s
TESTS=$MOUNT
LOGS=/logs/verifier
HERE=/grader/tests
ALT=/tmp/harbor-verify
fail() { echo "harbor verify: $*" >&2; exit 3; }
# fresh makes $1 an empty, writable folder, or fails.
fresh() { mkdir -p "$1" 2>/dev/null && find "$1" -mindepth 1 -delete 2>/dev/null && [ -w "$1" ]; }
REWRITE=0
if ! fresh "$TESTS" || ! fresh "$LOGS"; then
	# A read-only root file system: run from $ALT with the two paths rewritten
	# in test.sh. A test file that names them itself still reads the originals.
	echo "harbor verify: $TESTS or $LOGS is not writable; running from $ALT" >&2
	TESTS=$ALT/tests
	LOGS=$ALT/logs/verifier
	fresh "$TESTS" && fresh "$LOGS" || fail "cannot create $ALT"
	REWRITE=1
fi
cp -R "$HERE/upstream/." "$TESTS/" || fail "cannot copy the tests to $TESTS"
if [ "$REWRITE" = 1 ]; then
	sed -E -e "s#(^|[^A-Za-z0-9_./-])$MOUNT/#\\1$TESTS/#g" -e "s#(^|[^A-Za-z0-9_./-])/logs/verifier#\\1$LOGS#g" \
		"$HERE/harbor/test.sh" > "$TESTS/test.sh" || fail "cannot rewrite test.sh"
else
	cp "$HERE/harbor/test.sh" "$TESTS/test.sh" || fail "cannot copy test.sh"
fi
%[4]scd %[5]s || fail "cannot enter the working directory %[6]s"
if command -v bash >/dev/null 2>&1; then
	bash "$TESTS/test.sh"
else
	sh "$TESTS/test.sh"
fi
code=$?
if [ -f "$LOGS/reward.txt" ]; then
	cp "$LOGS/reward.txt" /grader/out/reward.json
elif [ -f "$LOGS/reward.json" ]; then
	# Harbor's reward.json may hold more keys than the bench's reward format
	# allows. Keep only "reward"; the whole file is kept as harbor-reward.json.
	cp "$LOGS/reward.json" /grader/out/harbor-reward.json
	r=$(tr -d '\n\r' < "$LOGS/reward.json" | sed -n 's/.*"reward"[[:space:]]*:[[:space:]]*\([-+0-9.eE]*\).*/\1/p')
	if [ -n "$r" ]; then
		printf '{"reward": %%s}\n' "$r" > /grader/out/reward.json
	else
		cp "$LOGS/reward.json" /grader/out/reward.json
	fi
fi
if [ -f "$LOGS/ctrf.json" ]; then
	cp "$LOGS/ctrf.json" /grader/out/ctrf.json
fi
exit $code
`, id, up.Benchmark, t.TestsMount, env.String(), shellQuote(workdir), workdir)
}

// referenceScript is the reference control: the upstream solution folder
// embedded as a tar, unpacked where Harbor uploads it, then solve.sh run from
// the task's working directory with the solution environment.
func referenceScript(t *upstreamTask, p *taskPlan) string {
	if p.draft != "" {
		return draftReference(p.draft)
	}
	var env strings.Builder
	for _, k := range sortedEnvKeys(t.SolutionEnv) {
		fmt.Fprintf(&env, "export %s=%s\n", k, shellQuote(t.SolutionEnv[k]))
	}
	var skills string
	if len(p.skillsTar) > 0 {
		quoted := make([]string, len(p.skillRoots))
		for i, r := range p.skillRoots {
			quoted[i] = shellQuote(r)
		}
		skills = fmt.Sprintf(`# The task's skills (skills/ beside this file), installed where the solution
# may look for them; a folder that cannot be written is left out.
base64 -d > /tmp/harbor-skills.tgz <<'HARBOR_SKILLS'
%sHARBOR_SKILLS
for d in %s; do
	mkdir -p "$d" 2>/dev/null && tar -xzf /tmp/harbor-skills.tgz -C "$d" 2>/dev/null || true
done
rm -f /tmp/harbor-skills.tgz
`, wrap76(p.skillsTar), strings.Join(quoted, " "))
	}
	return fmt.Sprintf(`#!/bin/sh
# Reference control written by bifrost-bench import harbor: the upstream
# solution folder (solution/ beside this file, embedded below), unpacked to
# %[1]s as Harbor uploads it, then solve.sh run from %[2]s. When %[1]s
# cannot be written (a non-root user without the folder baked into the
# image), it unpacks to /tmp/harbor-solution and rewrites %[1]s in solve.sh.
set -eu
MOUNT=%[1]s
SOL=$MOUNT
if ! { mkdir -p "$SOL" && [ -w "$SOL" ]; } 2>/dev/null; then
	SOL=/tmp/harbor-solution
	mkdir -p "$SOL"
fi
base64 -d > /tmp/harbor-solution.tgz <<'HARBOR_SOLUTION'
%[3]sHARBOR_SOLUTION
tar -xzf /tmp/harbor-solution.tgz -C "$SOL"
rm -f /tmp/harbor-solution.tgz
if [ "$SOL" != "$MOUNT" ]; then
	sed -E "s#(^|[^A-Za-z0-9_./-])$MOUNT/#\\1$SOL/#g" "$SOL/solve.sh" > "$SOL/solve.sh.tmp" && mv "$SOL/solve.sh.tmp" "$SOL/solve.sh"
fi
%[6]s%[4]scd %[5]s
if command -v bash >/dev/null 2>&1; then
	exec bash "$SOL/solve.sh"
fi
exec sh "$SOL/solve.sh"
`, t.SolutionMount, p.workdir, wrap76(p.solution), env.String(), shellQuote(p.workdir), skills)
}

// wrap76 is b in base64, in lines of 76 characters.
func wrap76(b []byte) string {
	enc := base64.StdEncoding.EncodeToString(b)
	var out strings.Builder
	for len(enc) > 76 {
		out.WriteString(enc[:76] + "\n")
		enc = enc[76:]
	}
	out.WriteString(enc + "\n")
	return out.String()
}

const emptyScript = `#!/bin/sh
# Empty control written by bifrost-bench import harbor: it changes nothing,
# so the upstream tests must fail.
exit 0
`

const exploitScript = `#!/bin/sh
# Exploit control written by bifrost-bench import harbor: it writes a passing
# reward where Harbor's verifier reads one, without doing the task. The
# verifier wrapper empties /logs/verifier before the tests run, so this must
# fail.
for d in /logs/verifier /tmp; do
	mkdir -p "$d" 2>/dev/null || continue
	echo 1 > "$d/reward.txt" 2>/dev/null || true
	echo '{"reward": 1}' > "$d/reward.json" 2>/dev/null || true
done
exit 0
`

// draftReference is the reference control of a draft task: it fails with
// the reason, so admission refuses the task and the reason shows in its
// admission record.
func draftReference(reason string) string {
	return "#!/bin/sh\n# Draft task (bifrost-bench import harbor -set unrunnable=draft): the bench\n# cannot admit it, so this control fails on purpose. See IMPORT.md.\necho " +
		shellQuote("draft: "+reason) + " >&2\nexit 1\n"
}

// importNotes is IMPORT.md: where the task came from and every change the
// import made to it, so a reader can judge the converted task.
func importNotes(id string, t *upstreamTask, p *taskPlan, up upstream) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\nImported by `bifrost-bench import harbor` from %s (%s", id, up.Benchmark, up.Source)
	if up.Revision != "" {
		fmt.Fprintf(&b, " at %s", up.Revision)
	}
	fmt.Fprintf(&b, "), upstream folder `%s`, %s layout. License: %s.\n\n", t.ID, t.Layout, up.License)
	if p.draft != "" {
		b.WriteString("## Draft\n\nThis task is in the suite as a draft, and admission refuses it: " + p.draft +
			". The reference control fails with that reason on purpose; `solution/` is the upstream folder when there is one, unused.\n\n")
	}
	b.WriteString("## Changes made by the import\n\n")
	b.WriteString("- The verifier runs `verifier/tests/harbor/verify.sh` in a snapshot of the agent's container (verifier mode snapshot). " +
		"It copies `verifier/tests/upstream` to " + t.TestsMount + ", replaces `test.sh` with `verifier/tests/harbor/test.sh` and copies the reward to `/grader/out/reward.json`.\n")
	if len(p.tests.Changes) == 0 {
		b.WriteString("- `test.sh` downloads nothing, so it runs unchanged.\n")
	}
	for _, c := range p.tests.Changes {
		fmt.Fprintf(&b, "- test.sh, %s\n", c)
	}
	if p.passThreshold > 0 {
		fmt.Fprintf(&b, "- The suite passes a grade at reward >= %g (`-set pass_threshold`), not Harbor's 1; the raw reward is kept on every grade (partial credit). Long-Horizon Terminal-Bench's paper counts a task solved at >= 0.95, so a pass here between %g and 0.95 is not a paper solve.\n", p.passThreshold, p.passThreshold)
	}
	for _, n := range p.sepNotes {
		b.WriteString("- " + n + "\n")
	}
	for _, c := range p.solveChanges {
		fmt.Fprintf(&b, "- %s. `solution/` is upstream's; the reference control embeds the rewritten solve.sh.\n", c)
	}
	b.WriteString("- The agent, the reference script and the verifier run as root (`environment.user: root`), as Harbor runs them.\n")
	if t.AgentUser != "" && t.AgentUser != "root" {
		fmt.Fprintf(&b, "- Upstream runs the agent as `%s` (`[agent] user`), not root; the bench runs it as root, so anything that relied on the agent being unprivileged (root-only files, a root daemon it cannot restart) no longer holds.\n", t.AgentUser)
	}
	if t.Healthcheck != "" {
		fmt.Fprintf(&b, "- Upstream runs `%s` (`[environment.healthcheck]`) before the agent starts; the bench does not, so whatever it starts is started on first use instead.\n", t.Healthcheck)
	}
	if p.agentS != seconds(t.AgentTimeoutS, defaultAgentS) {
		fmt.Fprintf(&b, "- The agent budget is %d s (`-set agent_timeout`, Harbor's override_timeout_sec), not task.toml's %.0f s.\n", p.agentS, t.AgentTimeoutS)
	}
	if t.ContinueUntilTimeout {
		b.WriteString("- `continue_until_timeout = true` (Long-Horizon Terminal-Bench): its modified Harbor runs the verifier when the agent stops and, unless it passed, resumes the agent with a bare rejection until the timeout. " +
			"The bench runs the agent once, up to `timeouts.agent_s`, and grades once, as stock Harbor does; an agent that stops early is not resumed, so scores can be lower than the paper's. The reward is kept on the grade (partial credit).\n")
	}
	b.WriteString("- The appended Dockerfile section also copies root's git settings to /etc/gitconfig and trusts every repository, because Harbor's agent is root and the bench's is uid 1000.\n")
	if p.skillCopy > 0 {
		fmt.Fprintf(&b, "- The Dockerfile copied the skills folder into the image (%d line(s)); those lines are disabled, and the skills are installed by the bench from `skills/` only when skills are enabled.\n", p.skillCopy)
	}
	if p.skills {
		b.WriteString("- `environment/skills` moved to `skills/` (Task.Skills) and is left out of the build context.\n")
	}
	if len(t.EnvEnv) > 0 {
		b.WriteString("- The task's environment variables are set with ENV at the end of the Dockerfile.\n")
	}
	if p.honeypot {
		b.WriteString("- Hack-verifiable task: `admin/` in the workspace holds the solution and the tests on purpose. The bench starts the agent with its own entrypoint, so the image's inotifywait watchers never run, " +
			"reading them is not detected, and the hidden_solution, read_tests and modify_tests keys of reward.json are dropped (kept in harbor-reward.json, but they read 0 whatever happened). " +
			"`admin/**` is protected, so changing the planted tests is an integrity violation.\n")
	}
	if t.Layout == LayoutTaskMD {
		b.WriteString("- BenchFlow's verifier hardening (deleting conftest.py files from the workspace before the tests) is not applied; the workspace is read-only while the verifier runs.\n")
	}
	if p.egressFetch != "" { // recorded internet (egress.go)
		b.WriteString(egressNotes(p.egressFetch))
	} else if t.AllowInternet {
		b.WriteString("- Upstream lets the agent use the internet; bench runs have no network except model calls.\n")
	}
	if p.compose != nil { // X2: how the compose file was mapped (compose.go)
		for _, n := range p.compose.notes {
			b.WriteString("- " + n + "\n")
		}
	}
	if p.draft == "" {
		b.WriteString("- The reference control embeds `solution/` and runs `solve.sh`; the empty control does nothing; the exploit control plants a fake reward.\n")
	} else {
		b.WriteString("- The reference control fails on purpose (draft); the empty control does nothing; the exploit control plants a fake reward.\n")
	}
	return b.String()
}

// checkExistingSuite refuses to extend a suite.yaml in dir that another
// importer or a person wrote, or that a Harbor import of another benchmark
// wrote: extending it would change how its existing tasks are graded and
// relabel their provenance.
func checkExistingSuite(dir, id string, up upstream) error {
	b, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("harbor: read existing suite %s: %w", id, err)
	}
	s := &schemas.Suite{}
	if err := yaml.Unmarshal(b, s); err != nil {
		return fmt.Errorf("harbor: read existing suite %s: %w", id, err)
	}
	switch {
	case s.Imported == nil || s.Imported.Importer != Name:
		return fmt.Errorf("harbor: suite %s exists and was not written by the %s importer; import into a new suite id", id, Name)
	case s.Grader != "verifier_command":
		return fmt.Errorf("harbor: suite %s exists with grader %s; import into a new suite id", id, s.Grader)
	case s.Imported.Benchmark != up.Benchmark:
		return fmt.Errorf("harbor: suite %s holds %s tasks, not %s; import into a new suite id", id, s.Imported.Benchmark, up.Benchmark)
	}
	return nil
}

// writeSuite writes suite.yaml, or extends the one already there with the
// new tasks, and the suite's small base image.
func writeSuite(dir, id string, up upstream, track string, tasks []*converted, passThreshold float64, now time.Time) error {
	s := &schemas.Suite{}
	path := filepath.Join(dir, "suite.yaml")
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, s); err != nil {
			return fmt.Errorf("harbor: read existing %s: %w", path, err)
		}
	}
	s.ID = id
	if s.Version == "" {
		s.Version = suiteVersion
	}
	s.Description = fmt.Sprintf("%s imported by bifrost-bench import harbor from %s. Each task builds its own image; the upstream test script grades a snapshot of the agent's container.", up.Benchmark, up.Source)
	s.Environment = "docker_workspace"
	s.Grader = "verifier_command"
	s.BaseImage = schemas.ImageSpec{Dockerfile: "base/Dockerfile", Tag: "bench/" + id + "-base:1"}
	if s.Workdir == "" {
		// Each task brings its own workspace path; the suite's is only a
		// default, kept as first written when a later import extends it.
		s.Workdir = commonWorkdir(tasks)
	}
	timeout := s.Verifier.TimeoutS
	for _, c := range tasks {
		timeout = max(timeout, c.verifierS+verifierMarginS)
	}
	s.Verifier = schemas.SuiteVerifier{
		Command:    []string{"/bin/sh", verifyScript},
		Restore:    []string{},
		TimeoutS:   timeout,
		Result:     schemas.VerifierResultReward,
		ResultPath: rewardResultPath,
		Mode:       schemas.VerifierSnapshot,
	}
	for _, c := range tasks {
		if !contains(s.TaskIDs, c.id) {
			s.TaskIDs = append(s.TaskIDs, c.id)
		}
	}
	if s.ProbeIDs == nil {
		s.ProbeIDs = []string{}
	}
	if s.Track != schemas.TrackSkills {
		s.Track = track
	}
	if passThreshold > 0 {
		s.Verifier.PassThreshold = passThreshold
	}
	s.Imported = &schemas.ImportRecord{Importer: Name, Benchmark: up.Benchmark, Source: up.Source, Revision: up.Revision, License: up.License, At: now.UTC().Truncate(time.Second)}
	if err := os.MkdirAll(filepath.Join(dir, "base"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "base", "Dockerfile"), []byte(baseDockerfile), 0o644); err != nil {
		return err
	}
	return writeYAML(path, s)
}

const baseDockerfile = `# Base image of a suite imported by bifrost-bench import harbor. Every task
# builds its own image from its environment/ folder; this image only serves
# the parts of the bench that still need one image per suite.
FROM ubuntu:24.04
`

// commonWorkdir is the workspace path most tasks use.
func commonWorkdir(tasks []*converted) string {
	count := map[string]int{}
	for _, c := range tasks {
		count[c.workdir]++
	}
	best := "/app"
	n := 0
	for wd, k := range count {
		if k > n || k == n && wd < best {
			best, n = wd, k
		}
	}
	return best
}

// writeExperiment writes the two-arm skill comparison for a skills suite.
func writeExperiment(dir, id, agent string) error {
	body := fmt.Sprintf(`# Skill comparison for the imported suite %[1]s, written by
# bifrost-bench import harbor: the same tasks with each task's skills installed
# (arm B) and with none (arm A, skills.enabled false), decided by the usual
# paired test. Run it after suite lock and suite admit:
#   bifrost-bench run benchmarks/%[1]s/%[2]s
name: %[1]s-skills
suite: %[1]s@%[3]s
base: %[4]s
arms:
  A: {label: without skills, set: {skills.enabled: false}}
  B: {label: with the task skills, set: {skills.enabled: true}}
primary: {condition: clean, metric: verified_success}
guards:
  - {rule: no_new_integrity_violation}
conditions:
  - {id: clean, tasks: all, trials: 2}
budget: {total_usd: 5}
verdict: {policy: paired_signflip, alpha: 0.05}
seed: 20261115
concurrency: 2
manifest_mode: agent_exposure
`, id, experimentFile, suiteVersion, agent)
	return os.WriteFile(filepath.Join(dir, experimentFile), []byte(body), 0o644)
}

// writeYAML marshals v and drops empty values, which the task and suite
// schemas refuse where a field is optional (an empty notes block, an unset
// digest), while keeping the empty lists the schemas require.
func writeYAML(path string, v any) error {
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return err
	}
	prune(&node)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// requiredLists are the keys whose empty list the schemas require.
var requiredLists = map[string]bool{"tasks": true, "probes": true, "required": true, "mutants": true}

// prune removes mapping entries whose value is an empty string, zero, false,
// null, an empty mapping, or an empty list the schemas do not require.
func prune(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		for _, c := range n.Content {
			prune(c)
		}
		return
	}
	kept := n.Content[:0]
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		prune(v)
		if isEmptyNode(v) || v.Kind == yaml.SequenceNode && len(v.Content) == 0 && !requiredLists[k.Value] {
			continue
		}
		kept = append(kept, k, v)
	}
	n.Content = kept
}

func isEmptyNode(v *yaml.Node) bool {
	switch v.Kind {
	case yaml.ScalarNode:
		switch v.Tag {
		case "!!str":
			return v.Value == ""
		case "!!int", "!!float":
			return v.Value == "0"
		case "!!bool":
			return v.Value == "false"
		case "!!null":
			return true
		}
	case yaml.MappingNode:
		return len(v.Content) == 0
	}
	return false
}

// copyTree copies the regular files and folders under src to dst, keeping
// the executable bit. Top-level entries named in skip are left out. A
// symlink is copied as a link only when it stays inside src; one that
// leaves it makes the task a skip, as the suite loader would refuse it.
func copyTree(src, dst string, skip map[string]bool) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if top := strings.Split(filepath.ToSlash(rel), "/")[0]; skip[top] && rel != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			resolved := filepath.Join(filepath.Dir(p), link)
			if filepath.IsAbs(link) || !inside(src, resolved) {
				return &skipError{reason: fmt.Sprintf("symlink %s -> %s leaves the task folder", filepath.ToSlash(rel), link)}
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return nil
	})
}

func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// tarGz packs the regular files and folders under dir into a gzip tar with
// fixed times and owners, so the same folder always packs to the same bytes
// and the suite lock hash of the reference control is stable.
func tarGz(dir string) ([]byte, error) { return tarGzWith(dir, nil) }

// tarGzWith is tarGz with the content of some files (by slash path relative
// to dir) replaced, for a solve.sh the import rewrote.
func tarGzWith(dir string, override map[string][]byte) ([]byte, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && (d.IsDir() || d.Type().IsRegular()) {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(gz)
	for _, p := range paths {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		h := &tar.Header{Name: filepath.ToSlash(rel), ModTime: time.Unix(0, 0), Format: tar.FormatPAX}
		switch {
		case info.IsDir():
			h.Typeflag, h.Name, h.Mode = tar.TypeDir, h.Name+"/", 0o755
		default:
			h.Typeflag, h.Size, h.Mode = tar.TypeReg, info.Size(), 0o644
			if b, ok := override[h.Name]; ok {
				h.Size = int64(len(b))
			}
			if info.Mode()&0o111 != 0 {
				h.Mode = 0o755
			}
		}
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			b, ok := override[h.Name]
			if !ok {
				if b, err = os.ReadFile(p); err != nil {
					return nil, err
				}
			}
			if _, err := tw.Write(b); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shellQuote quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
