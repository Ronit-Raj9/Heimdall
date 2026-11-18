package monitorcert

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Command monitor limits.
const (
	// LabelMonitor labels every monitor container with its certificate id,
	// beside bifrost-bench=1, so the janitor and an operator can find it.
	LabelMonitor = "bifrost-bench.monitor"

	defaultNamePrefix = "bench-monitor"
	monitorUser       = "65534:65534" // nobody: exists in every stock image
	maxMonitorStdout  = 1 << 20
	maxMonitorStderr  = 4 << 10
	cleanupTimeout    = 15 * time.Second
)

// commandMonitor runs the monitor image once per run with the run's
// requests on stdin, one JSON object per line, and reads one JSON answer per
// line from stdout. The container is sandboxed like an agent container
// (DESIGN.md 14): non-root, every capability dropped, no new privileges,
// pids, memory and CPU caps, a read-only root with a small tmpfs /tmp, no
// network at all, no bind mounts and no Docker socket. The image must
// already be on the host (--pull never), so certification never pulls.
type commandMonitor struct {
	docker  utils.Docker
	image   string
	argv    []string
	timeout time.Duration
	res     schemas.MonitorResources
	prefix  string
	certID  string
	logger  schemas.Logger
	seq     atomic.Int64
}

func newCommandMonitor(r *Resolved, d RunnerDeps) *commandMonitor {
	prefix := d.NamePrefix
	if prefix == "" {
		prefix = defaultNamePrefix
	}
	return &commandMonitor{
		docker:  d.Docker,
		image:   r.Config.Image,
		argv:    r.Argv(),
		timeout: time.Duration(r.Config.TimeoutS) * time.Second,
		res:     r.Config.Resources,
		prefix:  prefix,
		certID:  d.CertID,
		logger:  d.Logger,
	}
}

// Score implements Monitor.
func (m *commandMonitor) Score(ctx context.Context, reqs []schemas.MonitorRequest) ([]schemas.MonitorResponse, error) {
	var in bytes.Buffer
	enc := json.NewEncoder(&in)
	enc.SetEscapeHTML(false)
	for i := range reqs {
		if err := enc.Encode(&reqs[i]); err != nil {
			return nil, fmt.Errorf("encode monitor request: %w", err)
		}
	}
	name := fmt.Sprintf("%s-%s-%d", m.prefix, strings.ReplaceAll(m.certID, "_", "-"), m.seq.Add(1))
	defer m.remove(name)
	out, err := m.run(ctx, name, in.Bytes())
	if err != nil {
		return nil, err
	}
	return parseAnswers(out, len(reqs))
}

// runArgs is the docker argv of one monitor container.
func (m *commandMonitor) runArgs(name string) []string {
	args := []string{"run", "--rm", "-i", "--pull", "never", "--name", name,
		"--user", monitorUser,
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(m.res.PidsLimit),
		"--memory", strconv.Itoa(m.res.MemoryMB) + "m",
		"--cpus", strconv.FormatFloat(m.res.CPUs, 'f', -1, 64),
		"--read-only",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=16m",
		"--env", "HOME=/tmp",
		"--network", "none",
		"--log-driver", "none",
	}
	args = append(args, utils.LabelArgs("", "")...)
	if m.certID != "" {
		args = append(args, "--label", LabelMonitor+"="+m.certID)
	}
	args = append(args, m.image)
	return append(args, m.argv...)
}

// run starts the container with stdin and returns its capped stdout. A
// non-zero exit, a timeout or output over the cap is an error carrying a
// short stderr excerpt.
func (m *commandMonitor) run(ctx context.Context, name string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	bin := m.docker.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, m.runArgs(name)...)
	cmd.Stdin = bytes.NewReader(stdin)
	stdout := &capBuffer{max: maxMonitorStdout}
	stderr := &capBuffer{max: maxMonitorStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.WaitDelay = time.Second // a killed client's children must not hold the pipes open
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return nil, fmt.Errorf("monitor container %s: timed out after %s", name, m.timeout)
	case stdout.over:
		return nil, fmt.Errorf("monitor container %s: stdout over %d bytes", name, maxMonitorStdout)
	case err != nil:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("monitor container %s: exit %d: %s", name, exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("monitor container %s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// remove force-removes the container in case --rm did not run (a timeout
// kills the docker client, not the container). A missing container is the
// normal case and is ignored.
func (m *commandMonitor) remove(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	res, err := m.docker.Run(ctx, "rm", "-f", name)
	switch {
	case err != nil:
		m.logger.Warn("monitor container cleanup failed", "container", name, "error", err.Error())
	case res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container"):
		m.logger.Warn("monitor container cleanup failed", "container", name, "error", strings.TrimSpace(string(res.Stderr)))
	}
}

// parseAnswers reads want JSON answers, one per non-empty line.
func parseAnswers(out []byte, want int) ([]schemas.MonitorResponse, error) {
	var resps []schemas.MonitorResponse
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), maxMonitorStdout)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r schemas.MonitorResponse
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("monitor answer %d: %w", len(resps)+1, err)
		}
		resps = append(resps, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read monitor answers: %w", err)
	}
	if len(resps) != want {
		return nil, fmt.Errorf("monitor wrote %d answers for %d requests", len(resps), want)
	}
	return resps, nil
}

// capBuffer keeps at most max bytes and remembers that more arrived.
type capBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*capBuffer)(nil)
