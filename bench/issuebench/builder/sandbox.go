package builder

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Workdir is where the repository sits inside the build container.
const Workdir = "/testbed"

// Sandbox is one build container. The repository tree (no .git token, no
// credentials) is already copied to Workdir when the sandbox is returned.
type Sandbox interface {
	// Exec runs cmd in bash in Workdir and returns its exit code and the
	// combined output. err is for the sandbox itself failing, not cmd.
	Exec(ctx context.Context, cmd string, timeout time.Duration) (exit int, out string, err error)
	// Snapshot saves the container state and returns its id.
	Snapshot(ctx context.Context) (string, error)
	// Rollback restores a snapshot.
	Rollback(ctx context.Context, id string) error
	Close(ctx context.Context) error
}

// SandboxFactory starts a fresh sandbox from a base image with the
// repository at hostRepoDir copied in.
type SandboxFactory func(ctx context.Context, image, hostRepoDir string, env []string) (Sandbox, error)

// DockerConfig runs build sandboxes with the docker CLI on the worker.
type DockerConfig struct {
	Docker string // docker binary, default "docker"
	// Network is the build network. The operator creates it with egress to
	// package registries only (PyPI and files.pythonhosted.org for Python);
	// "" refuses to start, so a build never runs on an open network by
	// accident.
	Network string
	// Runtime is the OCI runtime; "" picks runsc (gVisor) when docker lists
	// it, else the default runtime.
	Runtime string
	Memory  string // default 4g
	CPUs    string // default 2
}

// DockerFactory returns a SandboxFactory backed by docker.
func DockerFactory(cfg DockerConfig) SandboxFactory {
	if cfg.Docker == "" {
		cfg.Docker = "docker"
	}
	if cfg.Memory == "" {
		cfg.Memory = "4g"
	}
	if cfg.CPUs == "" {
		cfg.CPUs = "2"
	}
	return func(ctx context.Context, image, hostRepoDir string, env []string) (Sandbox, error) {
		if cfg.Network == "" {
			return nil, errors.New("no build network configured: the builder needs a network with egress to package registries only")
		}
		rt := cfg.Runtime
		if rt == "" && dockerHasRuntime(ctx, cfg.Docker, "runsc") {
			rt = "runsc"
		}
		s := &dockerSandbox{cfg: cfg, runtime: rt, env: env}
		if err := s.start(ctx, image); err != nil {
			return nil, err
		}
		if _, err := s.run(ctx, 10*time.Minute, "cp", hostRepoDir+"/.", s.id+":"+Workdir); err != nil {
			_ = s.Close(ctx)
			return nil, fmt.Errorf("copy the repository in: %w", err)
		}
		// The tree is copied without its git remote config, so no token
		// that cloned it is ever inside.
		_, _, _ = s.Exec(ctx, "rm -f .git/config.bak; git config --unset-all http.extraheader 2>/dev/null; git remote remove origin 2>/dev/null; true", time.Minute)
		return s, nil
	}
}

func dockerHasRuntime(ctx context.Context, docker, name string) bool {
	out, err := exec.CommandContext(ctx, docker, "info", "--format", "{{json .Runtimes}}").Output()
	return err == nil && bytes.Contains(out, []byte(`"`+name+`"`))
}

type dockerSandbox struct {
	cfg     DockerConfig
	runtime string
	env     []string
	mu      sync.Mutex
	id      string
	images  []string
}

func (s *dockerSandbox) start(ctx context.Context, image string) error {
	args := []string{"run", "-d", "--network", s.cfg.Network, "--memory", s.cfg.Memory, "--cpus", s.cfg.CPUs,
		"--pids-limit", "2048", "--security-opt", "no-new-privileges", "-w", Workdir, "--label", "heimdall.issuebench=build"}
	if s.runtime != "" {
		args = append(args, "--runtime", s.runtime)
	}
	for _, e := range s.env {
		args = append(args, "-e", e)
	}
	args = append(args, image, "sleep", "infinity")
	out, err := s.run(ctx, 10*time.Minute, args...)
	if err != nil {
		return fmt.Errorf("start build container from %s: %w", image, err)
	}
	s.id = strings.TrimSpace(out)
	return nil
}

func (s *dockerSandbox) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.cfg.Docker, args...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err != nil {
		return buf.String(), fmt.Errorf("%w: %s", err, tail(buf.String(), 400))
	}
	return buf.String(), nil
}

func (s *dockerSandbox) Exec(ctx context.Context, cmd string, timeout time.Duration) (int, string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	enc := base64.StdEncoding.EncodeToString([]byte(cmd))
	c := exec.CommandContext(cctx, s.cfg.Docker, "exec", "-w", Workdir, s.id, "bash", "-lc", "echo "+enc+" | base64 -d > /tmp/.hb_cmd && bash /tmp/.hb_cmd")
	var buf bytes.Buffer
	c.Stdout, c.Stderr = &buf, &buf
	err := c.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return 124, buf.String() + "\n(command timed out)", nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), buf.String(), nil
	}
	if err != nil {
		return -1, buf.String(), err
	}
	return 0, buf.String(), nil
}

func (s *dockerSandbox) Snapshot(ctx context.Context) (string, error) {
	out, err := s.run(ctx, 10*time.Minute, "commit", s.id)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	s.mu.Lock()
	s.images = append(s.images, id)
	s.mu.Unlock()
	return id, nil
}

func (s *dockerSandbox) Rollback(ctx context.Context, id string) error {
	_, _ = s.run(ctx, time.Minute, "rm", "-f", s.id)
	return s.start(ctx, id)
}

func (s *dockerSandbox) Close(ctx context.Context) error {
	cctx := context.WithoutCancel(ctx)
	_, err := s.run(cctx, time.Minute, "rm", "-f", s.id)
	s.mu.Lock()
	imgs := s.images
	s.images = nil
	s.mu.Unlock()
	for _, im := range imgs {
		_, _ = s.run(cctx, time.Minute, "rmi", "-f", im)
	}
	return err
}
