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

	// Scenario inputs (applyScenario), all empty for a plain task.
	seedImage string // task image whose seedPath folder is the pristine workspace (instead of repoDir)
	seedPath  string
	snapshot  string // snapshot mode: image id of the frozen agent container, the verifier image
	workspace string // snapshot mode: the run's workspace volume, mounted read-only
	// separate is the verifier image of a separate verifier (snapshot mode
	// with environment.verifier_artifacts): the verifier runs there, with the
	// listed paths copied out of the snapshot to /grader/collect.
	separate   string
	artifacts  []string
	stateDir   string // <run folder>/state, mounted read-only at /grader/state
	answerFile string // <run folder>/answer.txt, mounted read-only at /grader/answer.txt
	// artifactsDir is <run folder>/artifacts, mounted read-only at
	// /grader/artifacts.
	artifactsDir string
	// trajectorySteps and trajectoryTools are the run's step timeline
	// (steps.jsonl) and tool proxy ledger (tools.jsonl), mounted read-only
	// at /grader/trajectory/, so a verifier can score the path as well as
	// the end state.
	trajectorySteps string
	trajectoryTools string
	root            bool // the task runs as root (environment.user root): so does its verifier
	// gpuArgs pass the run's GPUs to the verifier container (X7, gpu.go):
	// the preparation step never gets them.
	gpuArgs []string

	// X2: a compose task's verifier (compose.go): the workspace service's
	// variables, and the internal run networks of the services it queries.
	env      map[string]string
	networks []string
}

// gradeJob names the docker objects of one grade.
type gradeJob struct {
	expID, runID string
	in           *gradeInputs
	workspace    string
	out          string
	container    string
	pristine     string // volume holding the task image's workspace folder, when the task has one
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
// suite base image for both the preparation step and the verifier. The repo
// and the hidden tests are bind-mounted by their real paths inside the task
// folder, resolved here, right before the grade: docker follows symlinks, so
// a folder swapped for a link after the suite loaded must not mount a host
// folder outside the task.
func gatherInputs(rc *schemas.RunContext, diff *schemas.Diff, image string) (*gradeInputs, error) {
	if rc.Task == nil || rc.Task.Dir == "" || rc.Suite == nil {
		return nil, errors.New("run context needs a task folder and a suite")
	}
	if diff == nil || diff.Path == "" {
		return nil, errors.New("no diff to grade")
	}
	in := &gradeInputs{
		image:   image,
		command: rc.Suite.Verifier.Command,
		timeout: time.Duration(rc.Suite.Verifier.TimeoutS) * time.Second,
	}
	if in.image == "" {
		in.image = rc.Suite.BaseImage.Tag
	}
	in.prepImage = in.image
	gpuArgs, gerr := verifierGPUArgs(rc) // X7: the verifier of a GPU task gets the run's GPUs (gpu.go)
	if gerr != nil {
		return nil, gerr
	}
	in.gpuArgs = gpuArgs
	if in.timeout <= 0 {
		in.timeout = defaultVerifierS * time.Second
	}
	if in.image == "" || len(in.command) == 0 || rc.Task.Verifier.Tests == "" {
		return nil, errors.New("suite needs an image and a verifier command, and the task a tests folder")
	}
	var err error
	dirs := []string{}
	if !seededFromImage(rc) && utils.VerifierMode(rc.Suite) != schemas.VerifierSnapshot {
		if in.repoDir, err = utils.ResolveInside(rc.Task.Dir, rc.Task.RepoDir); err != nil {
			return nil, fmt.Errorf("task repo: %w", err)
		}
		dirs = append(dirs, in.repoDir)
	}
	if in.testsDir, err = utils.ResolveInside(rc.Task.Dir, rc.Task.Verifier.Tests); err != nil {
		return nil, fmt.Errorf("task verifier.tests: %w", err)
	}
	for _, d := range append(dirs, in.testsDir) {
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
	j := &gradeJob{
		expID:     expID,
		runID:     runID,
		in:        in,
		workspace: "bench-grade-" + runID + "-" + sfx,
		out:       "bench-gout-" + runID + "-" + sfx,
		container: "bench-" + runID + "-verify-" + sfx,
	}
	if in.seedImage != "" && in.snapshot == "" {
		j.pristine = "bench-gsrc-" + runID + "-" + sfx
	}
	return j
}

// run creates the grade's volumes, fills the workspace, runs the verifier
// and, when the patch applied and the verifier finished in time, calls
// collect while the stopped verifier container still exists. /grader/out is
// a volume created here, empty, for this grade only: neither the diff nor the
// restore step writes to it, so the agent cannot plant a result file. Every
// container and volume it creates is removed before it returns.
func (c *cleanRoom) run(ctx context.Context, j *gradeJob, collect collectFunc) (cleanResult, error) {
	defer c.cleanup(ctx, j)
	for _, v := range j.volumes() {
		args := append([]string{"volume", "create"}, utils.LabelArgs(j.expID, j.runID)...)
		if _, err := c.docker.MustRun(ctx, append(args, v)...); err != nil {
			return cleanResult{}, fmt.Errorf("create volume %s: %w", v, err)
		}
	}
	var applied bool
	var err error
	if j.in.separate != "" {
		applied, err = true, c.prepareSeparate(ctx, j)
	} else if j.in.snapshot != "" {
		applied, err = true, c.prepareSnapshot(ctx, j)
	} else {
		applied, err = c.prepare(ctx, j)
	}
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
	src := j.in.repoDir
	if j.pristine != "" {
		if err := c.seedPristine(ctx, j); err != nil {
			return false, err
		}
		src = j.pristine
	}
	args := []string{"run", "--rm", "--name", j.container + "-prep"}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "DAC_READ_SEARCH", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g", "--log-driver", "none",
		"-v", j.workspace+":/workspace",
		"-v", j.out+":/grader/out",
		"-v", src+":/bench-src:ro")
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
	image, access := j.in.image, ":ro"
	if j.in.separate != "" {
		// A separate verifier: its own image, the collected artifacts
		// read-only at /grader/collect, a writable root of its own.
		image, mount = j.in.separate, collectDir
	} else if j.in.snapshot != "" {
		// The grade's own copy of the run's workspace: writable, thrown away.
		image, access = j.in.snapshot, ""
	}
	args := []string{"create", "--name", j.container}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	if j.in.snapshot == "" {
		// A snapshot verifier keeps the frozen /tmp and a writable root of
		// its own (discarded with the container): the agent's result can
		// live anywhere in that file system.
		args = append(args, "--read-only", "--tmpfs", "/tmp:exec,size=512m")
	}
	if err := c.checkInternal(ctx, j.in.networks); err != nil { // X2: only internal run networks (compose.go)
		return 0, false, err
	}
	args = append(args, verifierNetworkArgs(j.in)...) // X2: none, unless a compose task's verifier queries services (compose.go)
	args = append(args, verifierEnvArgs(j.in)...)     // X2
	args = append(args, verifierIdentity(j.in.root)...)
	args = append(args,
		"--security-opt", "no-new-privileges",
		"--pids-limit", "512", "--memory", verifierMemory(j.in), "--memory-swap", verifierMemory(j.in), "--cpus", strconv.Itoa(min(2, runtime.NumCPU())), // X7: a GPU verifier gets more host memory (gpu.go)
		"-v", j.workspace+":"+mount+access,
		"-v", j.in.testsDir+":/grader/tests:ro",
		"-v", j.out+":/grader/out",
		"--log-driver", "none")
	args = append(args, scenarioMounts(j.in)...)
	args = append(args, j.in.gpuArgs...) // X7: GPU devices, verifier only (gpu.go)
	if j.in.workdir != "" {
		args = append(args, "--workdir", j.in.workdir)
	}
	args = append(args, "--entrypoint", j.in.command[0], image)
	args = append(args, j.in.command[1:]...)
	if _, err := c.docker.MustRun(ctx, args...); err != nil {
		return 0, false, fmt.Errorf("create verifier container: %w", err)
	}
	if err := c.connectExtra(ctx, j.container, j.in.networks); err != nil { // X2 (compose.go)
		return 0, false, err
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
	if res, err := c.docker.Run(cctx, "rm", "-f", "-v", j.container, j.container+"-prep", j.container+"-src", j.container+"-seed"); err != nil {
		c.logger.Warn("remove grading containers", "run", j.runID, "error", err)
	} else if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container") {
		c.logger.Warn("remove grading containers", "run", j.runID, "stderr", strings.TrimSpace(string(res.Stderr)))
	}
	if _, err := c.docker.MustRun(cctx, append([]string{"volume", "rm", "-f"}, j.volumes()...)...); err != nil {
		c.logger.Warn("remove grading volumes", "run", j.runID, "error", err)
	}
}

// volumes are the grade's own volumes: the grading workspace (in snapshot
// mode a copy of the run's), the out folder, and the pristine copy when the
// grade builds one.
func (j *gradeJob) volumes() []string {
	vols := []string{j.workspace, j.out}
	if j.pristine != "" {
		vols = append(vols, j.pristine)
	}
	return vols
}

// snapshotRefFile is the run file environments.Snapshot writes (the frozen
// agent container's image id); a Grader sees only the run context and the
// diff, so the snapshot reaches it through the run folder.
const snapshotRefFile = "snapshot.ref"

// GraderTrajectoryDir is where a verifier finds the run's trajectory:
// steps.jsonl (the step timeline) and tools.jsonl (the tool proxy ledger),
// each mounted read-only when the run folder holds it.
const GraderTrajectoryDir = "/grader/trajectory"

// trajectoryToolsFile is the tool proxy ledger in the run folder.
const trajectoryToolsFile = "tools.jsonl"

var snapshotRefRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// applyScenario adds a scenario task's inputs to a grade: in snapshot mode
// the frozen agent container (from snapshot.ref) and the run's workspace
// volume replace the clean-room build; a task with its own image is
// verified in its verifier_image, else its task image, and when the image
// seeds the workspace the pristine copy comes from it too; the exported
// service state, answer, artifact and trajectory files are mounted when the
// run folder holds them. A plain task is left as it was.
func applyScenario(rc *schemas.RunContext, in *gradeInputs) error {
	in.root = utils.TaskRunsAsRoot(rc.Task)
	if img, ok, err := utils.TaskImage(rc.Suite.ID, rc.Task); err != nil {
		return err
	} else if ok {
		in.image = img.Tag
		if v := rc.Task.Environment.VerifierImage; v != "" {
			in.image = v
		}
		if wp := rc.Task.Environment.WorkspacePath; wp != "" {
			if _, err := verifierWorkdir(wp); err != nil {
				return fmt.Errorf("task workspace_path: %w", err)
			}
			in.seedImage, in.seedPath = img.Tag, wp
			in.workdir = wp
		}
	}
	if utils.VerifierMode(rc.Suite) == schemas.VerifierSnapshot {
		ref, err := readSnapshotRef(rc.Dir)
		if err != nil {
			return err
		}
		if rc.Handle == nil || rc.Handle.Volume == "" {
			return errors.New("snapshot grading needs the run's workspace volume")
		}
		in.snapshot, in.workspace = ref, rc.Handle.Volume
		if e := rc.Task.Environment; e != nil && len(e.VerifierArtifacts) > 0 && e.VerifierImage != "" {
			arts := e.VerifierArtifacts
			for _, a := range arts {
				if !path.IsAbs(a) || strings.Contains(a, "..") || path.Clean(a) == "/" {
					return fmt.Errorf("verifier_artifacts path %q must be a clean absolute path below /", a)
				}
			}
			in.separate, in.artifacts = e.VerifierImage, arts
		}
	}
	if err := composeVerifier(rc, in); err != nil { // X2: environment and service networks of a compose task's verifier (compose.go)
		return err
	}
	if err := egressVerifier(rc, in); err != nil { // recorded internet: a verifier that downloads reaches the egress service (egress.go)
		return err
	}
	if rc.Dir == "" {
		return nil
	}
	if st, err := os.Lstat(filepath.Join(rc.Dir, "state")); err == nil && st.IsDir() {
		in.stateDir = filepath.Join(rc.Dir, "state")
	}
	if st, err := os.Lstat(filepath.Join(rc.Dir, "answer.txt")); err == nil && st.Mode().IsRegular() {
		in.answerFile = filepath.Join(rc.Dir, "answer.txt")
	}
	if st, err := os.Lstat(filepath.Join(rc.Dir, utils.ArtifactsDir)); err == nil && st.IsDir() {
		in.artifactsDir = filepath.Join(rc.Dir, utils.ArtifactsDir)
	}
	if st, err := os.Lstat(filepath.Join(rc.Dir, schemas.StepsFile)); err == nil && st.Mode().IsRegular() {
		in.trajectorySteps = filepath.Join(rc.Dir, schemas.StepsFile)
	}
	if st, err := os.Lstat(filepath.Join(rc.Dir, trajectoryToolsFile)); err == nil && st.Mode().IsRegular() {
		in.trajectoryTools = filepath.Join(rc.Dir, trajectoryToolsFile)
	}
	for _, p := range []*string{&in.stateDir, &in.answerFile, &in.artifactsDir, &in.trajectorySteps, &in.trajectoryTools} {
		if *p == "" {
			continue
		}
		abs, err := filepath.Abs(*p)
		if err != nil {
			return fmt.Errorf("run folder file %s: %w", *p, err)
		}
		*p = abs
	}
	return nil
}

// seededFromImage reports whether the task's workspace comes from a folder
// of its own image rather than its repo folder.
func seededFromImage(rc *schemas.RunContext) bool {
	return rc.Task != nil && rc.Task.Environment != nil && rc.Task.Environment.WorkspacePath != ""
}

// readSnapshotRef reads the image id Snapshot left in the run folder.
func readSnapshotRef(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("snapshot grading needs the run folder")
	}
	raw, err := os.ReadFile(filepath.Join(dir, snapshotRefFile))
	if err != nil {
		return "", fmt.Errorf("snapshot grading: %w", err)
	}
	ref := strings.TrimSpace(string(raw))
	if !snapshotRefRe.MatchString(ref) {
		return "", fmt.Errorf("snapshot grading: %s holds %q, not an image id", snapshotRefFile, truncate(ref, 80))
	}
	return ref, nil
}

// verifierIdentity is the verifier's user, capabilities and home: uid 1000
// with none and a tmpfs home, or, for a task that runs as root, root with
// utils.RootCaps and its own /root as home (upstream test scripts expect
// both). It is pure.
func verifierIdentity(root bool) []string {
	if !root {
		return []string{"--user", verifierUser, "--cap-drop", "ALL",
			"--tmpfs", "/home/agent:exec,size=64m,mode=0700,uid=1000,gid=1000", "-e", "HOME=/home/agent"}
	}
	args := []string{"--user", "0:0", "--cap-drop", "ALL"}
	for _, c := range utils.RootCaps {
		args = append(args, "--cap-add", c)
	}
	return append(args, "-e", "HOME=/root")
}

// scenarioMounts are the verifier's read-only mounts of the run's exported
// state, answer, artifact and trajectory files. It is pure.
func scenarioMounts(in *gradeInputs) []string {
	var args []string
	if in.stateDir != "" {
		args = append(args, "-v", in.stateDir+":/grader/state:ro")
	}
	if in.answerFile != "" {
		args = append(args, "-v", in.answerFile+":/grader/answer.txt:ro")
	}
	if in.artifactsDir != "" {
		args = append(args, "-v", in.artifactsDir+":"+utils.GraderArtifactsDir+":ro")
	}
	if in.trajectorySteps != "" {
		args = append(args, "-v", in.trajectorySteps+":"+GraderTrajectoryDir+"/"+schemas.StepsFile+":ro")
	}
	if in.trajectoryTools != "" {
		args = append(args, "-v", in.trajectoryTools+":"+GraderTrajectoryDir+"/"+trajectoryToolsFile+":ro")
	}
	return args
}

// prepareSnapshot copies the run's workspace volume (read-only) into the
// grade's own workspace volume and hands the fresh /grader/out to the
// verifier user. The verifier gets the copy, writable, because upstream test
// scripts write into their working folder; the run's volume is never
// changed, and the diff was exported from it before grading.
func (c *cleanRoom) prepareSnapshot(ctx context.Context, j *gradeJob) error {
	args := []string{"run", "--rm", "--name", j.container + "-prep"}
	args = append(args, utils.LabelArgs(j.expID, j.runID)...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "DAC_READ_SEARCH", "--cap-add", "FOWNER", "--cap-add", "FSETID",
		"--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g", "--log-driver", "none",
		"-v", j.in.workspace+":/bench-src:ro", "-v", j.workspace+":/workspace", "-v", j.out+":/grader/out",
		"--entrypoint", "/bin/sh", j.in.prepImage, "-c", "set -eu; cp -a /bench-src/. /workspace/; chown 1000:1000 /grader/out")
	if _, err := c.docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("prepare snapshot grading workspace: %w", err)
	}
	return nil
}

// collectDir is where a separate verifier sees the artifacts copied out of
// the agent's container, each at its own absolute path below it.
const collectDir = "/grader/collect"

// prepareSeparate copies each verifier artifact out of the frozen agent
// container (created from the snapshot with the run's workspace volume
// mounted read-only at the working folder, never started) into the grade's
// workspace volume at the same path. A path the agent did not create is left
// out: the verifier sees it missing, as Harbor's would.
func (c *cleanRoom) prepareSeparate(ctx context.Context, j *gradeJob) error {
	src := j.container + "-src"
	wd := j.in.workdir
	if wd == "" {
		wd = "/workspace"
	}
	create := append([]string{"create", "--name", src}, utils.LabelArgs(j.expID, j.runID)...)
	create = append(create, "--network", "none", "-v", j.in.workspace+":"+wd+":ro", "--entrypoint", "/bench-none", j.in.snapshot)
	if _, err := c.docker.MustRun(ctx, create...); err != nil {
		return fmt.Errorf("create snapshot container: %w", err)
	}
	for _, a := range j.in.artifacts {
		a = strings.TrimSuffix(path.Clean(a), "/")
		parent := path.Dir(a)
		to := []string{"run", "-i", "--rm", "--name", j.container + "-seed"}
		to = append(to, utils.LabelArgs(j.expID, j.runID)...)
		to = append(to, "--network", "none", "--user", "0", "--cap-drop", "ALL",
			"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER", "--cap-add", "FSETID",
			"--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g", "--log-driver", "none",
			"-v", j.workspace+":/workspace", "-v", j.out+":/grader/out",
			"--entrypoint", "/bin/sh", j.in.prepImage, "-c", `set -eu; mkdir -p "/workspace$1"; tar -x -f - -C "/workspace$1"; chown 1000:1000 /grader/out`, "bench-collect", parent)
		from := []string{"cp", src + ":" + a, "-"}
		if _, err := c.docker.Pipe(ctx, from, to); err != nil {
			c.logger.Debug("verifier artifact not collected", "run", j.runID, "path", a, "error", err)
		}
	}
	return nil
}

// seedPristine copies the task image's workspace folder into the grade's
// pristine volume. The image is created, never started, and the tar stream
// goes from docker cp straight into a trusted helper.
func (c *cleanRoom) seedPristine(ctx context.Context, j *gradeJob) error {
	src := j.container + "-src"
	create := append([]string{"create", "--name", src}, utils.LabelArgs(j.expID, j.runID)...)
	create = append(create, "--network", "none", "--entrypoint", "/bench-none", j.in.seedImage)
	if _, err := c.docker.MustRun(ctx, create...); err != nil {
		return fmt.Errorf("create task image container: %w", err)
	}
	to := []string{"run", "-i", "--rm", "--name", j.container + "-seed"}
	to = append(to, utils.LabelArgs(j.expID, j.runID)...)
	to = append(to, "--network", "none", "--user", "0", "--cap-drop", "ALL",
		"--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--pids-limit", "256", "--memory", "2g", "--log-driver", "none",
		"-v", j.pristine+":/bench-src", "--entrypoint", "tar", j.in.prepImage, "-x", "-f", "-", "-C", "/bench-src")
	from := []string{"cp", src + ":" + strings.TrimSuffix(j.in.seedPath, "/") + "/.", "-"}
	if _, err := c.docker.Pipe(ctx, from, to); err != nil {
		return fmt.Errorf("copy pristine workspace from %s:%s: %w", j.in.seedImage, j.in.seedPath, err)
	}
	return nil
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
