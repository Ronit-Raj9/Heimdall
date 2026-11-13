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

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
