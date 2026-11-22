package builder

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
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
	// Run runs every docker call (a shared worker passes
	// issuebench.ExecDocker(policy.Command) so the org's policy applies).
	// Nil execs Docker.
	Run issuebench.DockerFunc
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
	// Labels go on every container the sandbox creates (and so on its
	// snapshot images). heimdall.issuebench defaults to "build".
	Labels map[string]string
	// ExtraEnv (KEY=VALUE) is given to the container with -e only: proxy
	// and CA variables of the worker. It is never part of the builder's
	// env, so it never reaches the written Dockerfile.
	ExtraEnv []string
	// Files are copied into the container before it starts (container path
	// to content), for example the egress proxy's CA certificate.
	Files map[string][]byte
	// Log receives the one line said when snapshots fall back to replay
	// (nil: the standard logger).
	Log func(format string, args ...any)
}

// replayPrefix marks a snapshot id of replay mode: replay:<n>.
const replayPrefix = "replay:"

// replayNotice is logged once per (runtime, docker) whose commits lose changes.
const replayNotice = "this worker's container runtime does not keep changes on docker commit; the builder replays commands instead"

// snapshotChecks caches CheckSnapshots per (runtime, docker).
var snapshotChecks sync.Map // key string -> *snapshotCheck

type snapshotCheck struct {
	once sync.Once
	ok   bool
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
	if cfg.Run == nil {
		cfg.Run = issuebench.ExecDocker(func(ctx context.Context, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, cfg.Docker, args...)
		})
	}
	if cfg.Log == nil {
		cfg.Log = log.Printf
	}
	return func(ctx context.Context, image, hostRepoDir string, env []string) (Sandbox, error) {
		if cfg.Network == "" {
			return nil, errors.New("no build network configured: the builder needs a network with egress to package registries only")
		}
		rt := cfg.Runtime
		if rt == "" && dockerHasRuntime(ctx, cfg.Run, "runsc") {
			rt = "runsc"
		}
		c := cfg
		c.Runtime = rt
		key := cfg.Docker + "\x00" + rt
		v, _ := snapshotChecks.LoadOrStore(key, &snapshotCheck{})
		chk := v.(*snapshotCheck)
		chk.once.Do(func() {
			err := CheckSnapshots(ctx, c, image)
			chk.ok = err == nil
			if err != nil {
				cfg.Log("%s (%v)", replayNotice, err)
			}
		})
		s := &dockerSandbox{cfg: c, runtime: rt, env: append([]string(nil), env...), base: image, repo: hostRepoDir, replay: !chk.ok}
		if err := s.boot(ctx); err != nil {
			return nil, err
		}
		return s, nil
	}
}

func dockerHasRuntime(ctx context.Context, run issuebench.DockerFunc, name string) bool {
	out, err := run(ctx, nil, "info", "--format", "{{json .Runtimes}}")
	return err == nil && strings.Contains(string(out), `"`+name+`"`)
}

// snapshotCheckFile is what CheckSnapshots writes and reads back.
const snapshotCheckFile = "/tmp/.heimdall-snapshot-check"

// CheckSnapshots tells whether docker commit keeps a change made in a
// started container: it writes a file in a container of image, commits it,
// and reads the file from the committed image. Under some gVisor setups the
// change stays in the sandbox's own overlay and the commit misses it.
func CheckSnapshots(ctx context.Context, cfg DockerConfig, image string) error {
	if cfg.Run == nil {
		d := cfg.Docker
		if d == "" {
			d = "docker"
		}
		cfg.Run = issuebench.ExecDocker(func(ctx context.Context, args ...string) *exec.Cmd { return exec.CommandContext(ctx, d, args...) })
	}
	run := func(timeout time.Duration, args ...string) (string, error) {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		out, err := cfg.Run(cctx, nil, args...)
		return strings.TrimSpace(string(out)), err
	}
	cleanup := context.WithoutCancel(ctx)
	base := append([]string{"--network", "none"}, labelArgs(cfg.Labels)...)
	if cfg.Runtime != "" {
		base = append(base, "--runtime", cfg.Runtime)
	}
	id, err := run(10*time.Minute, append(append([]string{"create"}, base...), image, "sleep", "infinity")...)
	if err != nil {
		return fmt.Errorf("snapshot check: create: %w", err)
	}
	defer cfg.Run(cleanup, nil, "rm", "-f", id)
	if _, err := run(time.Minute, "start", id); err != nil {
		return fmt.Errorf("snapshot check: start: %w", err)
	}
	if _, err := run(time.Minute, "exec", id, "sh", "-c", "echo heimdall-snapshot-ok > "+snapshotCheckFile); err != nil {
		return fmt.Errorf("snapshot check: write: %w", err)
	}
	img, err := run(10*time.Minute, "commit", id)
	if err != nil {
		return fmt.Errorf("snapshot check: commit: %w", err)
	}
	defer cfg.Run(cleanup, nil, "rmi", "-f", img)
	out, err := run(time.Minute, append(append([]string{"run", "--rm"}, base...), img, "cat", snapshotCheckFile)...)
	if err != nil || !strings.Contains(out, "heimdall-snapshot-ok") {
		return fmt.Errorf("snapshot check: the committed image lost the change (%v)", err)
	}
	return nil
}

func labelArgs(labels map[string]string) []string {
	m := map[string]string{issuebench.LabelWork: "build"}
	for k, v := range labels {
		m[k] = v
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		out = append(out, "--label", k+"="+m[k])
	}
	return out
}

// replayStep is one Exec call that ran, kept for replay mode.
type replayStep struct {
	cmd     string
	timeout time.Duration
}

type dockerSandbox struct {
	cfg     DockerConfig
	runtime string
	env     []string
	base    string // the base image
	repo    string // host repository folder
	replay  bool   // snapshots replay commands instead of docker commit
	mu      sync.Mutex
	id      string
	images  []string
	steps   []replayStep
}

// boot creates a container from the base image, copies the repository and
// Files in, starts it, and drops any git remote config.
func (s *dockerSandbox) boot(ctx context.Context) error {
	if err := s.create(ctx, s.base); err != nil {
		return err
	}
	if _, err := s.run(ctx, 10*time.Minute, "cp", s.repo+"/.", s.id+":"+Workdir); err != nil {
		_ = s.Close(ctx)
		return fmt.Errorf("copy the repository in: %w", err)
	}
	if err := s.copyFiles(ctx); err != nil {
		_ = s.Close(ctx)
		return err
	}
	if _, err := s.run(ctx, 10*time.Minute, "start", s.id); err != nil {
		_ = s.Close(ctx)
		return fmt.Errorf("start build container from %s: %w", s.base, err)
	}
	// The tree is copied without its git remote config, so no token
	// that cloned it is ever inside.
	_, _, _ = s.exec(ctx, "rm -f .git/config.bak; git config --unset-all http.extraheader 2>/dev/null; git remote remove origin 2>/dev/null; true", time.Minute)
	return nil
}

// create makes the container (not started) from image.
func (s *dockerSandbox) create(ctx context.Context, image string) error {
	args := []string{"create", "--network", s.cfg.Network, "--memory", s.cfg.Memory, "--cpus", s.cfg.CPUs,
		"--pids-limit", "2048", "--security-opt", "no-new-privileges", "-w", Workdir}
	args = append(args, labelArgs(s.cfg.Labels)...)
	if s.runtime != "" {
		args = append(args, "--runtime", s.runtime)
	}
	for _, e := range s.env {
		args = append(args, "-e", e)
	}
	for _, e := range s.cfg.ExtraEnv {
		args = append(args, "-e", e)
	}
	args = append(args, image, "sleep", "infinity")
	out, err := s.run(ctx, 10*time.Minute, args...)
	if err != nil {
		return fmt.Errorf("create build container from %s: %w", image, err)
	}
	s.id = strings.TrimSpace(out)
	return nil
}

// chmodTree sets file to 0644 and every folder from root down to it to
// 0755, whatever the umask.
func chmodTree(root, file string) error {
	if err := os.Chmod(file, 0o644); err != nil {
		return err
	}
	for d := filepath.Dir(file); len(d) > len(root); d = filepath.Dir(d) {
		if err := os.Chmod(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// copyFiles copies cfg.Files into the created container through a temp
// folder laid out like the container's root.
func (s *dockerSandbox) copyFiles(ctx context.Context) error {
	if len(s.cfg.Files) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "heimdall-ib-files-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	tops := map[string]bool{}
	for p, b := range s.cfg.Files {
		clean := path.Clean("/" + p)
		if clean == "/" || clean != "/"+strings.TrimPrefix(p, "/") {
			return fmt.Errorf("sandbox file %q is not a clean absolute path", p)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return err
		}
		// docker cp keeps modes, and the caller's umask (077 on the shared
		// worker) would make these 0600 files in 0700 folders, which a
		// non-root user in the container (or /etc itself) would then lose.
		if err := chmodTree(tmp, dst); err != nil {
			return err
		}
		tops[strings.SplitN(strings.TrimPrefix(clean, "/"), "/", 2)[0]] = true
	}
	// One cp per top-level entry into "/", so the container's root folder
	// itself keeps its own mode.
	names := make([]string, 0, len(tops))
	for n := range tops {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := s.run(ctx, 5*time.Minute, "cp", filepath.Join(tmp, n), s.id+":/"); err != nil {
			return fmt.Errorf("copy files into the build container: %w", err)
		}
	}
	return nil
}

func (s *dockerSandbox) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := s.cfg.Run(cctx, nil, args...)
	if err != nil {
		var ee *issuebench.ExitError
		if errors.As(err, &ee) {
			return string(out), err
		}
		return string(out), fmt.Errorf("%w: %s", err, tail(string(out), 400))
	}
	return string(out), nil
}

func (s *dockerSandbox) Exec(ctx context.Context, cmd string, timeout time.Duration) (int, string, error) {
	code, out, err := s.exec(ctx, cmd, timeout)
	if err == nil && s.replay {
		s.mu.Lock()
		s.steps = append(s.steps, replayStep{cmd: cmd, timeout: timeout})
		s.mu.Unlock()
	}
	return code, out, err
}

func (s *dockerSandbox) exec(ctx context.Context, cmd string, timeout time.Duration) (int, string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The command goes in on stdin, never in argv: a state overlay carries
	// whole files (base64), and one argv string over 128 KiB fails with
	// "argument list too long" before docker runs (a 97 KB CHANGELOG did).
	out, err := s.cfg.Run(cctx, []byte(cmd), "exec", "-i", "-w", Workdir, s.id, "bash", "-lc", "cat > /tmp/.hb_cmd && bash /tmp/.hb_cmd </dev/null")
	if ctx.Err() != nil {
		return -1, string(out), ctx.Err()
	}
	if cctx.Err() == context.DeadlineExceeded {
		return 124, string(out) + "\n(command timed out)", nil
	}
	var ee *issuebench.ExitError
	if errors.As(err, &ee) {
		return ee.Code, string(out), nil
	}
	if err != nil {
		return -1, string(out), err
	}
	return 0, string(out), nil
}

func (s *dockerSandbox) Snapshot(ctx context.Context) (string, error) {
	if s.replay {
		s.mu.Lock()
		defer s.mu.Unlock()
		return replayPrefix + strconv.Itoa(len(s.steps)), nil
	}
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
	_, _ = s.run(context.WithoutCancel(ctx), time.Minute, "rm", "-f", s.id)
	if strings.HasPrefix(id, replayPrefix) {
		n, err := strconv.Atoi(strings.TrimPrefix(id, replayPrefix))
		s.mu.Lock()
		if err != nil || n < 0 || n > len(s.steps) {
			s.mu.Unlock()
			return fmt.Errorf("unknown snapshot %q", id)
		}
		steps := append([]replayStep(nil), s.steps[:n]...)
		s.steps = steps
		s.mu.Unlock()
		if err := s.boot(ctx); err != nil {
			return err
		}
		for i, st := range steps {
			if _, _, err := s.exec(ctx, st.cmd, st.timeout); err != nil {
				return fmt.Errorf("replay command %d of %d: %w", i+1, n, err)
			}
		}
		return nil
	}
	if err := s.create(ctx, id); err != nil {
		return err
	}
	if err := s.copyFiles(ctx); err != nil {
		return err
	}
	if _, err := s.run(ctx, 10*time.Minute, "start", s.id); err != nil {
		return fmt.Errorf("start build container from %s: %w", id, err)
	}
	return nil
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
