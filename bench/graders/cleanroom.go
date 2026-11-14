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

const (
	defaultVerifierS  = 300
	patchNotApplied   = 42
	maxVerifierOutput = 64 << 10
	maxCopyDrain      = 1 << 20
	verifierUser      = "1000:1000"
	verifierNetwork   = "none"
	cleanupTimeout    = 60 * time.Second
	// ReasonPatchDoesNotApply is the Grade.Reason when the diff does not
	// apply to the pristine repo. It is a fail, not an invalid evaluator.
	ReasonPatchDoesNotApply = "patch does not apply"
	// ReasonTestRunBroken prefixes a fail where the patch applied but the test run produced no usable report.
	ReasonTestRunBroken = "test run broken by the patch"
)

var runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// cleanRoom runs a suite's verifier on an exported diff in fresh containers.
// hidden_tests and verifier_command share it, so every suite, whatever its
// language, is graded under the same isolation: a fresh workspace volume
// filled from the pristine repo, the diff applied by a trusted helper with no
// network, the restore list reset, and the verifier run with no network, the
// workspace read-only, the hidden verifier files read-only at /grader/tests
// and a fresh, empty /grader/out that no workspace file can reach.
type cleanRoom struct {
	docker utils.Docker
	logger schemas.Logger
}

// gradeInputs are the validated host paths and settings of one grade.
type gradeInputs struct {
	prepImage string // runs the trusted preparation step; needs sh, cp and git
	image     string // runs the verifier
	repoDir   string
	testsDir  string
	diffPath  string
	diffSHA   string
	empty     bool
	command   []string
	restore   []string
	timeout   time.Duration
	workdir   string // where the verifier sees the workspace; empty keeps /workspace and the image's working directory
}

// gradeJob names the docker objects of one grade.
type gradeJob struct {
	expID, runID string
	in           *gradeInputs
	workspace    string
	out          string
	container    string
}

// cleanResult is what one clean-room run observed.
type cleanResult struct {
	applied  bool
	exitCode int
	timedOut bool
}

// collectFunc reads what a grader needs from the stopped verifier container
// before the container is removed.
type collectFunc func(ctx context.Context, container string) error

// tooLargeError is a result file over its byte limit. A grader decides
// whether that is a broken test run or an error.
type tooLargeError struct {
	size, limit int64
}

// Error implements error.
func (e *tooLargeError) Error() string {
	return fmt.Sprintf("%d bytes, over the %d byte limit", e.size, e.limit)
}

// gatherInputs validates the run context and the diff, and checks the diff
// file still has the hash the export recorded. image, when set, replaces the
// suite base image for both the preparation step and the verifier.
func gatherInputs(rc *schemas.RunContext, diff *schemas.Diff, image string) (*gradeInputs, error) {
	if rc.Task == nil || rc.Task.Dir == "" || rc.Suite == nil {
		return nil, errors.New("run context needs a task folder and a suite")
	}
	if diff == nil || diff.Path == "" {
		return nil, errors.New("no diff to grade")
	}
	in := &gradeInputs{
		image:    image,
		repoDir:  filepath.Join(rc.Task.Dir, rc.Task.RepoDir),
		testsDir: filepath.Join(rc.Task.Dir, rc.Task.Verifier.Tests),
		command:  rc.Suite.Verifier.Command,
		timeout:  time.Duration(rc.Suite.Verifier.TimeoutS) * time.Second,
	}
	if in.image == "" {
		in.image = rc.Suite.BaseImage.Tag
	}
	in.prepImage = in.image
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

// newGradeJob names the volumes and containers of one grade. The random
// suffix keeps a regrade of the same run from colliding with a leftover.
func newGradeJob(expID, runID string, in *gradeInputs) *gradeJob {
	sfx := strings.TrimPrefix(utils.NewID("g", 6), "g_")
	return &gradeJob{
		expID:     expID,
		runID:     runID,
		in:        in,
		workspace: "bench-grade-" + runID + "-" + sfx,
		out:       "bench-gout-" + runID + "-" + sfx,
		container: "bench-" + runID + "-verify-" + sfx,
	}
}

// run creates the grade's volumes, fills the workspace, runs the verifier
// and, when the patch applied and the verifier finished in time, calls
// collect while the stopped verifier container still exists. /grader/out is
// a volume created here, empty, for this grade only: neither the diff nor the
// restore step writes to it, so the agent cannot plant a result file. Every
// container and volume it creates is removed before it returns.
func (c *cleanRoom) run(ctx context.Context, j *gradeJob, collect collectFunc) (cleanResult, error) {
	defer c.cleanup(ctx, j)
	for _, v := range []string{j.workspace, j.out} {
		args := append([]string{"volume", "create"}, utils.LabelArgs(j.expID, j.runID)...)
		if _, err := c.docker.MustRun(ctx, append(args, v)...); err != nil {
			return cleanResult{}, fmt.Errorf("create volume %s: %w", v, err)
		}
	}
	applied, err := c.prepare(ctx, j)
	if err != nil || !applied {
		return cleanResult{}, err
	}
	code, timedOut, err := c.verify(ctx, j)
	if err != nil {
		return cleanResult{}, err
	}
	res := cleanResult{applied: true, exitCode: code, timedOut: timedOut}
	if timedOut {
		return res, nil
	}
	if err := collect(ctx, j.container); err != nil {
		return cleanResult{}, err
	}
	return res, nil
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
func (c *cleanRoom) prepare(ctx context.Context, j *gradeJob) (bool, error) {
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
	args = append(args, "--entrypoint", "/bin/sh", j.in.prepImage, "-c", prepareScript, "bench-prep")
	args = append(args, j.in.restore...)
	res, err := c.docker.Run(ctx, args...)
	if err != nil {
		return false, fmt.Errorf("prepare grading workspace: %w", err)
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case patchNotApplied:
		c.logger.Debug("patch does not apply", "run", j.runID, "stderr", strings.TrimSpace(string(res.Stderr)))
		return false, nil
	default:
		return false, fmt.Errorf("prepare grading workspace: exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
}

// verify runs the verifier command in a fresh hardened container and returns
// its exit code and whether it timed out. Its output is kept only in a small
// capped buffer for the debug log: the run folder may already be sealed when
// this is a regrade.
func (c *cleanRoom) verify(ctx context.Context, j *gradeJob) (int, bool, error) {
	mount := "/workspace"
	if j.in.workdir != "" {
		mount = j.in.workdir
	}
	args := []string{"create", "--name", j.container}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	args = append(args,
		"--network", verifierNetwork, "--user", verifierUser,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "512", "--memory", "2g", "--memory-swap", "2g", "--cpus", strconv.Itoa(min(2, runtime.NumCPU())),
		"--read-only", "--tmpfs", "/tmp:exec,size=512m", "--tmpfs", "/home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000",
		"-e", "HOME=/home/agent",
		"-v", j.workspace+":"+mount+":ro",
		"-v", j.in.testsDir+":/grader/tests:ro",
		"-v", j.out+":/grader/out",
		"--log-driver", "none")
	if j.in.workdir != "" {
		args = append(args, "--workdir", j.in.workdir)
	}
	args = append(args, "--entrypoint", j.in.command[0], j.in.image)
	args = append(args, j.in.command[1:]...)
	if _, err := c.docker.MustRun(ctx, args...); err != nil {
		return 0, false, fmt.Errorf("create verifier container: %w", err)
	}
	output := &cappedBuffer{limit: maxVerifierOutput}
	cmd := exec.Command(dockerBinary(c.docker), "start", "-a", j.container)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		return 0, false, fmt.Errorf("start verifier: %w", err)
	}
	timedOut := c.waitOrKill(ctx, cmd, j.container, j.in.timeout)
	if ctx.Err() != nil {
		return 0, false, fmt.Errorf("verifier interrupted: %w", ctx.Err())
	}
	res, err := c.docker.MustRun(ctx, "inspect", "--format", "{{.State.ExitCode}}", j.container)
	if err != nil {
		return 0, false, fmt.Errorf("inspect verifier: %w", err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if err != nil {
		return 0, false, fmt.Errorf("inspect verifier: parse exit code: %w", err)
	}
	c.logger.Debug("verifier finished", "run", j.runID, "exit", code, "timed_out", timedOut, "output", output.String())
	return code, timedOut, nil
}

// waitOrKill waits for the attached client, killing the container when the
// timeout or ctx ends. It reports whether the timeout expired.
func (c *cleanRoom) waitOrKill(ctx context.Context, cmd *exec.Cmd, name string, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil {
			c.logger.Debug("docker start -a ended", "container", name, "error", err)
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
	if _, err := c.docker.Run(kctx, "kill", name); err != nil {
		c.logger.Warn("kill verifier", "container", name, "error", err)
	}
	select {
	case <-done:
	case <-kctx.Done():
		if err := cmd.Process.Kill(); err != nil {
			c.logger.Warn("kill docker client", "container", name, "error", err)
		}
		<-done
	}
	return timedOut
}

// readFile copies one result file out of the stopped verifier container.
// Only a regular file counts; a symlink, a folder or a missing file reads as
// nil. The tar stream is read in memory and never unpacked, so entry names
// cannot traverse anywhere. A file over limit is a *tooLargeError, and a
// stream that keeps going after the first entry (a folder full of files) is
// cut off instead of drained. label and noun name the file in errors, for
// example "junit" and "junit report".
func (c *cleanRoom) readFile(ctx context.Context, container, file string, limit int64, label, noun string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, dockerBinary(c.docker), "cp", container+":"+file, "-")
	stderr := &cappedBuffer{limit: maxVerifierOutput}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("read %s: %w", label, err)
	}
	data, rerr := readTarFile(stdout, limit)
	killed := false
	if n, err := io.CopyN(io.Discard, stdout, maxCopyDrain); err == nil && n == maxCopyDrain {
		if err := cmd.Process.Kill(); err != nil {
			c.logger.Debug("stop docker cp", "error", err)
		}
		killed = true
	} else if err != nil && err != io.EOF {
		c.logger.Debug("drain docker cp", "error", err)
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("read %s: %w", label, ctx.Err())
	}
	if werr != nil && !killed {
		msg := stderr.String()
		if strings.Contains(msg, "Could not find the file") || strings.Contains(msg, "No such") {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w: %s", label, werr, strings.TrimSpace(msg))
	}
	var tl *tooLargeError
	if errors.As(rerr, &tl) {
		return nil, fmt.Errorf("read %s: %s is %w", label, noun, rerr)
	}
	if rerr != nil {
		return nil, fmt.Errorf("read %s: %w", label, rerr)
	}
	return data, nil
}

// readTarFile returns the content of the first entry of a tar stream when it
// is a regular file of at most limit bytes, and nil for any other entry type.
// A larger file is a *tooLargeError.
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
		return nil, &tooLargeError{size: hdr.Size, limit: limit}
	}
	return io.ReadAll(io.LimitReader(tr, limit))
}

// cleanup removes the grade's containers and volumes even after ctx ended.
func (c *cleanRoom) cleanup(ctx context.Context, j *gradeJob) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if res, err := c.docker.Run(cctx, "rm", "-f", "-v", j.container, j.container+"-prep"); err != nil {
		c.logger.Warn("remove grading containers", "run", j.runID, "error", err)
	} else if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container") {
		c.logger.Warn("remove grading containers", "run", j.runID, "stderr", strings.TrimSpace(string(res.Stderr)))
	}
	if _, err := c.docker.MustRun(cctx, "volume", "rm", "-f", j.workspace, j.out); err != nil {
		c.logger.Warn("remove grading volumes", "run", j.runID, "error", err)
	}
}

// timedOutReason is the fail reason of a verifier that hit its timeout. The
// reference passed this verifier at admission, so a hang after a cleanly
// applied patch is the patch's doing: a fail, never unknown.
func timedOutReason(timeout time.Duration) string {
	return fmt.Sprintf("%s: verifier timed out after %s", ReasonTestRunBroken, timeout)
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
