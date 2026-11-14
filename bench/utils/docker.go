package utils

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Label keys every bench-created docker object carries.
const (
	LabelBench = "bifrost-bench"
	LabelRun   = "bifrost-bench.run"
	LabelExp   = "bifrost-bench.exp"
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
	bin := d.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
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
	res, err := d.Run(ctx, args...)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("docker %s: exit %d: %s", firstArg(args), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res, nil
}

// LabelArgs returns --label flags for the bench label set.
func LabelArgs(expID, runID string) []string {
	args := []string{"--label", LabelBench + "=1"}
	if expID != "" {
		args = append(args, "--label", LabelExp+"="+expID)
	}
	if runID != "" {
		args = append(args, "--label", LabelRun+"="+runID)
	}
	return args
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
