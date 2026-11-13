// Package graders holds the bench's Grader implementations. A grader decides
// pass or fail from the exported diff alone, in fresh containers, never from
// anything the agent's own container reports.
package graders

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// HiddenTestsType is the part name suites use to select this grader.
const HiddenTestsType = "hidden_tests"

const (
	hiddenTestsVersion = "1.0.0"
	defaultVerifierS   = 300
	patchNotApplied    = 42
	maxJUnitBytes      = 16 << 20
	maxVerifierOutput  = 64 << 10
	verifierUser       = "1000:1000"
	cleanupTimeout     = 60 * time.Second
	// ReasonPatchDoesNotApply is the Grade.Reason when the diff does not
	// apply to the pristine repo. It is a fail, not an invalid evaluator.
	ReasonPatchDoesNotApply = "patch does not apply"
	// ReasonTestRunBroken prefixes a fail where the patch applied but the test run produced no usable report.
	ReasonTestRunBroken = "test run broken by the patch"
)

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// HiddenTestsConfig configures the hidden_tests grader. Image runs both the
// trusted preparation step (it needs git and sh) and the verifier; when empty
// the suite base image is used.
type HiddenTestsConfig struct {
	Image  string
	Logger schemas.Logger
	Docker utils.Docker
}

// hiddenTests reproduces benchmarks/coding-core/tools/checktask.sh clean
// grading in Go.
type hiddenTests struct {
	cfg HiddenTestsConfig
}

// newHiddenTests builds the grader and fills config defaults.
func newHiddenTests(cfg HiddenTestsConfig) *hiddenTests {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	return &hiddenTests{cfg: cfg}
}

// GetName implements schemas.Part.
func (g *hiddenTests) GetName() string { return HiddenTestsType }

// GetVersion implements schemas.Part.
func (g *hiddenTests) GetVersion() string { return hiddenTestsVersion }

// Grade copies the pristine task repo into a fresh volume, applies the diff,
// restores the suite's restore paths from the pristine repo, and runs the
// suite's verifier command with no network, the workspace read-only and the
// hidden tests mounted read-only. A diff that does not apply is a fail. No
// JUnit report, zero tests or a verifier timeout make the evaluator invalid.
// Every container and volume it creates is removed before it returns.
func (g *hiddenTests) Grade(ctx context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	in, err := g.inputs(rc, diff)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	grade := &schemas.Grade{
		ExitCode:      -1,
		SuiteVersion:  rc.Suite.Version,
		GraderVersion: hiddenTestsVersion,
		DiffSHA256:    in.diffSHA,
	}
	sfx := strings.TrimPrefix(utils.NewID("g", 6), "g_")
	j := &gradeJob{
		expID:     expID,
		runID:     runID,
		in:        in,
		workspace: "bench-grade-" + runID + "-" + sfx,
		out:       "bench-gout-" + runID + "-" + sfx,
		container: "bench-" + runID + "-verify-" + sfx,
	}
	defer g.cleanup(ctx, j)
	for _, v := range []string{j.workspace, j.out} {
		args := append([]string{"volume", "create"}, utils.LabelArgs(expID, runID)...)
		if _, err := g.cfg.Docker.MustRun(ctx, append(args, v)...); err != nil {
			return nil, fmt.Errorf("grade run %s: create volume %s: %w", runID, v, err)
		}
	}
	applied, err := g.prepare(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	if !applied {
		grade.Reason = ReasonPatchDoesNotApply
		return grade, nil
	}
	code, timedOut, err := g.verify(ctx, j)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	if timedOut {
		// The reference passed this verifier at admission, so a hang after a
		// cleanly applied patch is the patch's doing: a fail, never unknown.
		grade.ExitCode = code
		grade.Reason = fmt.Sprintf("%s: verifier timed out after %s", ReasonTestRunBroken, in.timeout)
		return grade, nil
	}
	report, err := g.readJUnit(ctx, j.container)
	if err != nil {
		return nil, fmt.Errorf("grade run %s: %w", runID, err)
	}
	scoreGrade(grade, code, report, rc.Task.Verifier.Required)
	g.cfg.Logger.Debug("graded", "run", runID, "passed", grade.Passed, "reason", grade.Reason)
	return grade, nil
}

// gradeInputs are the validated host paths and settings of one grade.
type gradeInputs struct {
	image    string
	repoDir  string
	testsDir string
	diffPath string
	diffSHA  string
	empty    bool
	command  []string
	restore  []string
	timeout  time.Duration
}

// gradeJob names the docker objects of one grade.
type gradeJob struct {
	expID, runID string
	in           *gradeInputs
	workspace    string
	out          string
	container    string
}

// inputs validates the run context and the diff, and checks the diff file
// still has the hash the export recorded.
func (g *hiddenTests) inputs(rc *schemas.RunContext, diff *schemas.Diff) (*gradeInputs, error) {
	if rc.Task == nil || rc.Task.Dir == "" || rc.Suite == nil {
		return nil, errors.New("run context needs a task folder and a suite")
	}
	if diff == nil || diff.Path == "" {
		return nil, errors.New("no diff to grade")
	}
	in := &gradeInputs{
		image:    g.cfg.Image,
		repoDir:  filepath.Join(rc.Task.Dir, rc.Task.RepoDir),
		testsDir: filepath.Join(rc.Task.Dir, rc.Task.Verifier.Tests),
		command:  rc.Suite.Verifier.Command,
		timeout:  time.Duration(rc.Suite.Verifier.TimeoutS) * time.Second,
	}
	if in.image == "" {
		in.image = rc.Suite.BaseImage.Tag
	}
	if in.timeout <= 0 {
		in.timeout = defaultVerifierS * time.Second
	}
	if in.image == "" || len(in.command) == 0 || rc.Task.Verifier.Tests == "" {
		return nil, errors.New("suite needs an image and a verifier command, and the task a tests folder")
	}
	for _, d := range []string{in.repoDir, in.testsDir} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("%s is not a folder", d)
		}
	}
	for _, p := range rc.Suite.Verifier.Restore {
		if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." {
			return nil, fmt.Errorf("restore path %q must be a clean relative path", p)
		}
	}
	in.restore = rc.Suite.Verifier.Restore
	abs, err := filepath.Abs(diff.Path)
	if err != nil {
		return nil, fmt.Errorf("diff path %s: %w", diff.Path, err)
	}
	in.diffPath = abs
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("diff %s: %w", abs, err)
	}
	in.empty = st.Size() == 0
	if in.diffSHA, err = utils.SHA256File(abs); err != nil {
		return nil, err
	}
	if diff.SHA256 != "" && diff.SHA256 != in.diffSHA {
		return nil, fmt.Errorf("diff %s has sha256 %s, the export recorded %s", abs, in.diffSHA, diff.SHA256)
	}
	return in, nil
}

// prepareScript runs as root in a trusted helper with no network. The
// pristine repo and the patch are read-only bind mounts of trusted host
// files; the restore paths arrive as positional parameters.
const prepareScript = `set -eu
cp -a /bench-src/. /workspace/
cd /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp GIT_CEILING_DIRECTORIES=/
if [ -n "${BENCH_PATCH:-}" ]; then
	if ! git -c core.hooksPath=/dev/null apply -p1 --whitespace=nowarn "$BENCH_PATCH"; then exit 42; fi
fi
for p in "$@"; do
	rm -rf "/workspace/$p"
	if [ -e "/bench-src/$p" ] || [ -L "/bench-src/$p" ]; then
		mkdir -p "$(dirname "/workspace/$p")"
		cp -a "/bench-src/$p" "/workspace/$p"
	fi
done
chown -R 1000:1000 /workspace /grader/out
`

// prepare fills the grading volume. It reports false when the patch does not
// apply.
func (g *hiddenTests) prepare(ctx context.Context, j *gradeJob) (bool, error) {
	args := []string{"run", "--rm", "--name", j.container + "-prep"}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "DAC_READ_SEARCH", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g", "--log-driver", "none",
		"-v", j.workspace+":/workspace",
		"-v", j.out+":/grader/out",
		"-v", j.in.repoDir+":/bench-src:ro")
	if !j.in.empty {
		args = append(args, "-v", j.in.diffPath+":/bench-patch/workspace.diff:ro", "-e", "BENCH_PATCH=/bench-patch/workspace.diff")
	}
	args = append(args, "--entrypoint", "/bin/sh", j.in.image, "-c", prepareScript, "bench-prep")
	args = append(args, j.in.restore...)
	res, err := g.cfg.Docker.Run(ctx, args...)
	if err != nil {
		return false, fmt.Errorf("prepare grading workspace: %w", err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case patchNotApplied:
		g.cfg.Logger.Debug("patch does not apply", "run", j.runID, "stderr", strings.TrimSpace(string(res.Stderr)))
		return false, nil
	default:
		return false, fmt.Errorf("prepare grading workspace: exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
}

// verify runs the verifier command in a fresh hardened container and returns
// its exit code and whether it timed out. Its output is kept only in a small
// capped buffer for the debug log: the run folder may already be sealed when
// this is a regrade.
func (g *hiddenTests) verify(ctx context.Context, j *gradeJob) (int, bool, error) {
	args := []string{"create", "--name", j.container}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	args = append(args,
		"--network", "none", "--user", verifierUser,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "512", "--memory", "2g", "--memory-swap", "2g", "--cpus", strconv.Itoa(min(2, runtime.NumCPU())),
		"--read-only", "--tmpfs", "/tmp:exec,size=512m", "--tmpfs", "/home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000",
		"-e", "HOME=/home/agent",
		"-v", j.workspace+":/workspace:ro",
		"-v", j.in.testsDir+":/grader/tests:ro",
		"-v", j.out+":/grader/out",
		"--log-driver", "none",
		"--entrypoint", j.in.command[0], j.in.image)
	args = append(args, j.in.command[1:]...)
	if _, err := g.cfg.Docker.MustRun(ctx, args...); err != nil {
		return 0, false, fmt.Errorf("create verifier container: %w", err)
	}
	output := &cappedBuffer{limit: maxVerifierOutput}
	cmd := exec.Command(dockerBinary(g.cfg.Docker), "start", "-a", j.container)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return 0, false, fmt.Errorf("start verifier: %w", err)
	}
	timedOut := g.waitOrKill(ctx, cmd, j.container, j.in.timeout)
	if ctx.Err() != nil {
		return 0, false, fmt.Errorf("verifier interrupted: %w", ctx.Err())
	}
	res, err := g.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{.State.ExitCode}}", j.container)
	if err != nil {
		return 0, false, fmt.Errorf("inspect verifier: %w", err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if err != nil {
		return 0, false, fmt.Errorf("inspect verifier: parse exit code: %w", err)
	}
	g.cfg.Logger.Debug("verifier finished", "run", j.runID, "exit", code, "timed_out", timedOut, "output", output.String())
	return code, timedOut, nil
}

// waitOrKill waits for the attached client, killing the container when the
// timeout or ctx ends. It reports whether the timeout expired.
func (g *hiddenTests) waitOrKill(ctx context.Context, cmd *exec.Cmd, name string, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil {
			g.cfg.Logger.Debug("docker start -a ended", "container", name, "error", err)
		}
		close(done)
	}()
	wctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case <-done:
		return false
	case <-wctx.Done():
	}
	timedOut := ctx.Err() == nil
	kctx, kcancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer kcancel()
	if _, err := g.cfg.Docker.Run(kctx, "kill", name); err != nil {
		g.cfg.Logger.Warn("kill verifier", "container", name, "error", err)
	}
	select {
	case <-done:
	case <-kctx.Done():
		if err := cmd.Process.Kill(); err != nil {
			g.cfg.Logger.Warn("kill docker client", "container", name, "error", err)
		}
		<-done
	}
	return timedOut
}

// readJUnit copies junit.xml out of the stopped verifier container. Only a
// regular file counts; a symlink or a missing file reads as no report.
func (g *hiddenTests) readJUnit(ctx context.Context, container string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, dockerBinary(g.cfg.Docker), "cp", container+":/grader/out/junit.xml", "-")
	stderr := &cappedBuffer{limit: maxVerifierOutput}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("read junit: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("read junit: %w", err)
	}
	data, rerr := readTarFile(stdout, maxJUnitBytes)
	if _, err := io.Copy(io.Discard, stdout); err != nil {
		g.cfg.Logger.Debug("drain docker cp", "error", err)
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("read junit: %w", ctx.Err())
	}
	if werr != nil {
		msg := stderr.String()
		if strings.Contains(msg, "Could not find the file") || strings.Contains(msg, "No such") {
			return nil, nil
		}
		return nil, fmt.Errorf("read junit: %w: %s", werr, strings.TrimSpace(msg))
	}
	if rerr != nil {
		return nil, fmt.Errorf("read junit: %w", rerr)
	}
	return data, nil
}

// readTarFile returns the content of the first entry of a tar stream when it
// is a regular file of at most limit bytes, and nil for any other entry type.
func readTarFile(r io.Reader, limit int64) ([]byte, error) {
	tr := tar.NewReader(r)
	hdr, err := tr.Next()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if hdr.Typeflag != tar.TypeReg {
		return nil, nil
	}
	if hdr.Size > limit {
		return nil, fmt.Errorf("junit report is %d bytes, over the %d byte limit", hdr.Size, limit)
	}
	return io.ReadAll(io.LimitReader(tr, limit))
}

// cleanup removes the grade's containers and volumes even after ctx ended.
func (g *hiddenTests) cleanup(ctx context.Context, j *gradeJob) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if res, err := g.cfg.Docker.Run(cctx, "rm", "-f", "-v", j.container, j.container+"-prep"); err != nil {
		g.cfg.Logger.Warn("remove grading containers", "run", j.runID, "error", err)
	} else if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container") {
		g.cfg.Logger.Warn("remove grading containers", "run", j.runID, "stderr", strings.TrimSpace(string(res.Stderr)))
	}
	if _, err := g.cfg.Docker.MustRun(cctx, "volume", "rm", "-f", j.workspace, j.out); err != nil {
		g.cfg.Logger.Warn("remove grading volumes", "run", j.runID, "error", err)
	}
}

// scoreGrade fills grade from the verifier exit code and its JUnit report,
// following checktask.sh: pass only with exit 0, more than zero tests and no
// failures, errors or skips, plus every required test run and passed.
//
// The reference passed this verifier at admission, so when a cleanly applied
// patch leaves no report, an unreadable report or zero tests, the patch broke
// the test run and the grade is a fail. Otherwise an agent could turn a 0 into
// an unknown by crashing pytest. Only a broken required-test list, a task
// configuration problem, is an invalid evaluator. It is pure.
func scoreGrade(grade *schemas.Grade, exitCode int, junitXML []byte, required []string) {
	grade.ExitCode = exitCode
	if len(bytes.TrimSpace(junitXML)) == 0 {
		grade.Reason = fmt.Sprintf("%s: no junit report (exit %d)", ReasonTestRunBroken, exitCode)
		return
	}
	r, err := parseJUnit(junitXML)
	if err != nil {
		grade.Reason = fmt.Sprintf("%s: %v", ReasonTestRunBroken, err)
		return
	}
	grade.TestsRan, grade.Failures, grade.Errors, grade.Skipped = r.Tests, r.Failures, r.Errors, r.Skipped
	for _, c := range r.Cases {
		if c.Failed || c.Errored {
			grade.FailedTests = append(grade.FailedTests, c.ID())
		}
	}
	if r.Tests == 0 {
		grade.Reason = fmt.Sprintf("%s: no tests ran (exit %d)", ReasonTestRunBroken, exitCode)
		return
	}
	req, err := checkRequired(r, required)
	if err != nil {
		grade.InvalidEvaluator = true
		grade.Reason = err.Error()
		return
	}
	grade.MissingRequired = req.Missing
	var why []string
	if exitCode != 0 {
		why = append(why, fmt.Sprintf("exit %d", exitCode))
	}
	for _, c := range []struct {
		n    int
		name string
	}{{r.Failures, "failures"}, {r.Errors, "errors"}, {r.Skipped, "skipped"}} {
		if c.n > 0 {
			why = append(why, fmt.Sprintf("%d %s", c.n, c.name))
		}
	}
	if len(req.Missing) > 0 {
		why = append(why, "required tests did not run: "+strings.Join(req.Missing, ", "))
	}
	if len(req.Failed) > 0 {
		why = append(why, "required tests failed: "+strings.Join(req.Failed, ", "))
	}
	grade.Passed = len(why) == 0
	if grade.Passed {
		grade.Reason = fmt.Sprintf("passed (%d tests)", r.Tests)
		return
	}
	grade.Reason = strings.Join(why, "; ")
}

// cappedBuffer keeps the first limit bytes written to it and drops the rest
// without error, so a chatty verifier never blocks or fills memory.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

// Write implements io.Writer. It always reports the full length as written.
func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// String returns what was kept.
func (c *cappedBuffer) String() string { return c.buf.String() }

// runIDs returns the experiment and run ids that label every docker object.
func runIDs(rc *schemas.RunContext) (string, string, error) {
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return "", "", errors.New("run context has no run id")
	}
	runID := rc.Run.RunID
	if !runIDRe.MatchString(runID) {
		return "", "", fmt.Errorf("run id %q is not usable in docker names", runID)
	}
	expID := rc.Run.ExpID
	if expID == "" && rc.Slot != nil {
		expID = rc.Slot.ExpID
	}
	if expID == "" && rc.Experiment != nil {
		expID = rc.Experiment.ID
	}
	return expID, runID, nil
}

func dockerBinary(d utils.Docker) string {
	if d.Binary != "" {
		return d.Binary
	}
	return "docker"
}
