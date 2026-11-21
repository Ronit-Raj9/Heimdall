package utils

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
)

// Label keys every bench-created docker object carries.
const (
	LabelBench = "bifrost-bench"
	LabelRun   = "bifrost-bench.run"
	// LabelProject names the project (run store) whose run created a
	// per-run object, so a janitor of another project on the same docker
	// host never removes it (SetProject).
	LabelProject = "bifrost-bench.project"
	LabelExp     = "bifrost-bench.exp"
)

// DockerResult is the outcome of one docker CLI call.
type DockerResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Docker runs the docker CLI. The zero value uses "docker" from PATH.
type Docker struct {
	Binary string
}

// Run executes `docker args...`. A non-zero exit is returned in ExitCode with a
// nil error; err is non-nil only when docker could not run or ctx ended.
func (d Docker) Run(ctx context.Context, args ...string) (*DockerResult, error) {
	return d.RunEnv(ctx, nil, args...)
}

// RunEnv is Run with extra NAME=value pairs layered over the process
// environment of the docker CLI, so a `-e NAME` flag copies a value that
// must never appear on a command line (org secrets, G01).
func (d Docker) RunEnv(ctx context.Context, env []string, args ...string) (*DockerResult, error) {
	cmd := exec.CommandContext(ctx, d.bin(), args...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := &DockerResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return res, nil
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		res.ExitCode = exitErr.ExitCode()
		return res, nil
	default:
		return res, fmt.Errorf("docker %s: %w", firstArg(args), err)
	}
}

// MustRun is Run that turns a non-zero exit into an error carrying stderr.
func (d Docker) MustRun(ctx context.Context, args ...string) (*DockerResult, error) {
	return d.MustRunEnv(ctx, nil, args...)
}

// MustRunEnv is MustRun with RunEnv's extra environment.
func (d Docker) MustRunEnv(ctx context.Context, env []string, args ...string) (*DockerResult, error) {
	res, err := d.RunEnv(ctx, env, args...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("docker %s: exit %d: %s", firstArg(args), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res, nil
}

// Pipe runs `docker from...` with its stdout connected to the stdin of
// `docker to...`, so a tar stream moves from one container (or image) to
// another without touching the host file system, and returns the stdout of
// `docker to`. Both commands must exit 0; the error carries the stderr of the
// one that failed.
func (d Docker) Pipe(ctx context.Context, from, to []string) ([]byte, error) {
	src := exec.CommandContext(ctx, d.bin(), from...)
	dst := exec.CommandContext(ctx, d.bin(), to...)
	var srcErr, dstOut, dstErr bytes.Buffer
	src.Stderr, dst.Stdout, dst.Stderr = &srcErr, &dstOut, &dstErr
	r, err := src.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w", firstArg(from), err)
	}
	dst.Stdin = r
	if err := src.Start(); err != nil {
		return nil, fmt.Errorf("docker %s: %w", firstArg(from), err)
	}
	derr := dst.Run()
	if derr != nil {
		// The reader is gone; stop the writer so it does not block forever.
		if kerr := src.Process.Kill(); kerr != nil && !errors.Is(kerr, os.ErrProcessDone) {
			derr = errors.Join(derr, kerr)
		}
	}
	serr := src.Wait()
	switch {
	case serr != nil && (derr == nil || srcErr.Len() > 0):
		return nil, fmt.Errorf("docker %s: %w: %s", firstArg(from), serr, strings.TrimSpace(srcErr.String()))
	case derr != nil:
		return nil, fmt.Errorf("docker %s: %w: %s", firstArg(to), derr, strings.TrimSpace(dstErr.String()))
	}
	return dstOut.Bytes(), nil
}

// bin is the docker binary to run.
func (d Docker) bin() string {
	if d.Binary == "" {
		return "docker"
	}
	return d.Binary
}

// LabelArgs returns --label flags for the bench label set. A per-run object
// also carries the process's project label when SetProject set one.
func LabelArgs(expID, runID string) []string {
	args := []string{"--label", LabelBench + "=1"}
	if expID != "" {
		args = append(args, "--label", LabelExp+"="+expID)
	}
	if runID != "" {
		args = append(args, "--label", LabelRun+"="+runID)
		if p := Project(); p != "" {
			args = append(args, "--label", LabelProject+"="+p)
		}
	}
	return args
}

var project atomic.Value // string

// SetProject sets the project id this process labels its per-run docker
// objects with: one id per run store, so two projects sharing a docker
// host tell their runs apart (ProjectID).
func SetProject(id string) { project.Store(id) }

// Project returns the id SetProject set, or "".
func Project() string {
	s, _ := project.Load().(string)
	return s
}

// ProjectID is the project id of a run store folder: a short hash of its
// absolute path.
func ProjectID(runsRoot string) string {
	if abs, err := filepath.Abs(runsRoot); err == nil {
		runsRoot = abs
	}
	sum := sha256.Sum256([]byte(filepath.Clean(runsRoot)))
	return hex.EncodeToString(sum[:8])
}

// HelperTools are the commands the bench runs inside the trusted helper image
// (images.helper): sh, cp and git prepare, commit and export workspaces,
// python3 runs the egress probe, the network observer and the hidden_tests
// grader, and sleep holds the observer's network namespace. A toolchain image
// picked as the helper (a node or golang image, say) usually lacks python3,
// and every one of those steps then fails in a way that names something else.
var HelperTools = []string{"sh", "cp", "git", "python3", "sleep"}

// HelperToolsScript is a POSIX sh script that prints "missing <tool>" for
// every entry of HelperTools the image lacks, and nothing else.
const HelperToolsScript = `for t in sh cp git python3 sleep; do command -v "$t" >/dev/null 2>&1 || echo "missing $t"; done`

// MissingTools reads the output of HelperToolsScript (or any output with
// "missing <tool>" lines) and returns the tools named, in order.
func MissingTools(out string) []string {
	var missing []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "missing" {
			missing = append(missing, f[1])
		}
	}
	return missing
}

// HelperLacks is the one message every part uses for a helper image without
// a tool it needs, so doctor, the egress probe and the observer say the same
// thing: "helper image <tag> has no python3; set images.helper to the bench
// helper image".
func HelperLacks(image string, tools ...string) string {
	return fmt.Sprintf("helper image %s has no %s; set images.helper to the bench helper image", image, strings.Join(tools, " or "))
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
