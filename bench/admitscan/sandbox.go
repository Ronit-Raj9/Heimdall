package admitscan

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Container paths of the sandbox. The subject's home folder is a Docker
// volume (never a host folder) that holds the canary credentials, the
// subject's own files and a project folder with a .env file, which is the
// working folder of every run.
const (
	SandboxHome    = "/home/agent"
	SandboxProject = "/home/agent/project"
)

// Sandbox defaults.
const (
	DefaultSandboxUser = "1000:1000"
	defaultPidsLimit   = 256
	defaultMemoryMB    = 512
	defaultCPUs        = "1"
	defaultRunWall     = 120 * time.Second
	runOutputMax       = 1 << 20
	logsMax            = 1 << 20
	watchLinesMax      = 4 << 20
	watchReadyWait     = 30 * time.Second
	watchStopWait      = 10 * time.Second
	cleanupWait        = 30 * time.Second
	labelAdmit         = "bifrost-bench.admit"
)

// Dynamic finding locations and limits (DynamicFindings). The rule ids are
// declared with the other rules in skill.go.
const (
	sbFileNetwork     = "network"
	sbFileOutput      = "output"
	sbExcerptMax      = 200
	sbCanaryRedaction = "[CANARY:%s]"
)

// Watch event kinds (WatchEvent.T).
const (
	WatchReady   = "ready"
	WatchOpen    = "open"
	WatchAccess  = "access"
	WatchDirOpen = "dir_open"
	WatchDNS     = "dns"
	WatchSYN     = "syn"
	WatchUDP     = "udp"
	WatchARP     = "arp"
	WatchError   = "error"
	WatchStats   = "stats"
)

// sandboxWatchScript watches canary files and network attempts; see its
// docstring.
//
//go:embed sandboxwatch.py
var sandboxWatchScript string

// sandboxRouteScript adds the on-link default route that makes outbound
// connection attempts visible as ARP requests; see its docstring.
//
//go:embed sandboxroute.py
var sandboxRouteScript string

// sandboxPlantScript extracts the planted tar from stdin into the home
// volume with the owners it carries, then hands the volume root to the
// sandbox user (argv: uid gid).
const sandboxPlantScript = `import os, sys, tarfile
uid, gid = int(sys.argv[1]), int(sys.argv[2])
kw = {"filter": "fully_trusted"} if hasattr(tarfile, "fully_trusted_filter") else {}
with tarfile.open(fileobj=sys.stdin.buffer, mode="r|") as t:
    t.extractall("/h", numeric_owner=True, **kw)
os.chown("/h", uid, gid)
os.chmod("/h", 0o755)
print("ok")
`

// credentialDirs are the folders whose listing is reported (relative to
// the home folder).
var credentialDirs = []string{".ssh", ".aws", ".config/gh"}

// SandboxConfig configures a Sandbox. Image is required; every other field
// has a default.
type SandboxConfig struct {
	Docker      utils.Docker
	Logger      schemas.Logger
	Prefix      string        // docker object name prefix; default bench-admit-<random>
	Image       string        // the image the subject runs in
	HelperImage string        // python3 image for planting, the route, the watcher and Attach; default DefaultHelperImage
	User        string        // uid:gid the subject runs as and owns its home; default DefaultSandboxUser
	Files       []PlantedFile // planted under the home folder before the watcher starts
	PidsLimit   int           // default 256
	MemoryMB    int           // default 512
	CPUs        string        // default "1"
}

// PlantedFile is one file written into the sandbox home before the subject
// starts.
type PlantedFile struct {
	Path string // relative to the home folder, slash-separated
	Mode int64  // permission bits; default 0644
	Data []byte
}

// Canary is one fake credential planted in the sandbox home. Value is unique
// to the sandbox, so finding it anywhere proves where it came from.
type Canary struct {
	ID    string // ssh_key | aws | gh | netrc | dotenv
	Path  string // container path
	Value string
}

// RunOutput is the outcome of one Sandbox.Run.
type RunOutput struct {
	ExitCode int
	TimedOut bool
	Output   []byte // stdout and stderr interleaved, capped at 1 MiB
	Total    int64  // bytes of output produced, including what the cap dropped
	Duration time.Duration
}

// WatchEvent is one line of the watcher's output.
type WatchEvent struct {
	T        string `json:"t"` // ready | open | access | dir_open | dns | syn | udp | arp | error | stats
	Path     string `json:"path,omitempty"`
	Dst      string `json:"dst,omitempty"`
	Port     int    `json:"port,omitempty"`
	Name     string `json:"name,omitempty"`
	QType    int    `json:"qtype,omitempty"`
	Resolver bool   `json:"resolver,omitempty"` // an ARP request for one of Docker's upstream DNS servers
	Error    string `json:"error,omitempty"`
	Files    int    `json:"files,omitempty"` // ready: canary files watched
	Dirs     int    `json:"dirs,omitempty"`  // ready: credential folders watched
}

// Observation is what the watcher saw. Observed is false when the watcher
// could not run or stopped early; the run then proves nothing.
type Observation struct {
	Observed bool
	Error    string
	Events   []WatchEvent
}

// Sandbox is a set of Docker objects that runs an untrusted subject (a
// skill's setup scripts, an MCP server) with canary credentials in its home
// folder and a watcher that records every open of a canary file and every
// network attempt. The subject gets the agent container hardening of
// DESIGN.md section 14: a non-root user, no capabilities, no new
// privileges, process, memory and CPU limits, a read-only root with a tmpfs
// /tmp, no host bind mounts, and an internal network with no route out.
//
// The network namespace belongs to a holder container on an internal
// network; the subject, the watcher and Attach clients join it. A one-shot
// container with only CAP_NET_ADMIN adds an on-link default route there, so
// a connection to an outside address sends an ARP request the watcher sees
// (and then fails) instead of failing silently with no packet.
type Sandbox struct {
	cfg      SandboxConfig
	uid, gid int
	prefix   string
	network  string
	volume   string
	holder   string
	watcher  string
	server   string
	canaries []Canary

	mu         sync.Mutex
	names      []string // every container created, for Close
	runs       int
	attaches   int
	watch      *exec.Cmd
	watchLines []string
	watchBytes int
	watchErr   bytes.Buffer
	watchDone  chan struct{}
	observed   *Observation
	closeOnce  sync.Once
	closeErr   error
}

// Attached is a helper process running in the sandbox's network namespace
// with its stdin and stdout connected to the caller.
type Attached struct {
	Stdin  io.WriteCloser
	Stdout io.Reader
	name   string
	cmd    *exec.Cmd
	d      utils.Docker
	stderr *sbCappedBuffer
	once   sync.Once
	err    error
}

// NewSandbox creates the network, the home volume with canaries and the
// configured files, the holder with its route, and the watcher, and returns
// once the watcher is ready. On any failure every object made so far is
// removed.
func NewSandbox(ctx context.Context, cfg SandboxConfig) (*Sandbox, error) {
	s, err := newSandboxState(cfg)
	if err != nil {
		return nil, err
	}
	steps := []struct {
		what string
		fn   func(context.Context) error
	}{
		{"create network", s.createNetwork},
		{"create home volume", s.createVolume},
		{"plant home", s.plant},
		{"start holder", s.startHolder},
		{"add route", s.addRoute},
		{"start watcher", s.startWatcher},
	}
	for _, st := range steps {
		began := time.Now()
		err := st.fn(ctx)
		s.logger().Debug("admission sandbox step", "sandbox", s.prefix, "step", st.what, "ms", time.Since(began).Milliseconds())
		if err != nil {
			if cerr := s.Close(ctx); cerr != nil {
				err = errors.Join(err, cerr)
			}
			return nil, fmt.Errorf("sandbox %s: %s: %w", s.prefix, st.what, err)
		}
	}
	return s, nil
}

// Canaries returns the planted canary credentials.
func (s *Sandbox) Canaries() []Canary {
	return append([]Canary(nil), s.canaries...)
}

// Run runs argv (argv[0] as the entrypoint) in a one-shot subject container
// and waits for it, at most wall (default 120 s), after which the container
// is removed and TimedOut is set. A non-zero exit is not an error.
func (s *Sandbox) Run(ctx context.Context, argv []string, workdir string, env map[string]string, wall time.Duration) (RunOutput, error) {
	if len(argv) == 0 {
		return RunOutput{}, errors.New("run: empty argv")
	}
	s.mu.Lock()
	s.runs++
	name := fmt.Sprintf("%s-run-%d", s.prefix, s.runs)
	s.names = append(s.names, name)
	s.mu.Unlock()
	args, err := s.subjectArgs(name, workdir, env, false)
	if err != nil {
		return RunOutput{}, err
	}
	args = append(args, "--entrypoint", argv[0], s.cfg.Image)
	args = append(args, argv[1:]...)
	if wall <= 0 {
		wall = defaultRunWall
	}
	out := &sbCappedBuffer{max: runOutputMax}
	cmd := exec.Command(sbDockerBin(s.cfg.Docker), args...)
	cmd.Stdout, cmd.Stderr = out, out
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return RunOutput{}, fmt.Errorf("run %s: %w", name, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(wall)
	defer timer.Stop()
	var werr error
	res := RunOutput{}
	select {
	case werr = <-done:
	case <-timer.C:
		res.TimedOut = true
		s.removeContainers(ctx, name)
		werr = <-done
	case <-ctx.Done():
		s.removeContainers(ctx, name)
		<-done
		return RunOutput{}, fmt.Errorf("run %s: %w", name, ctx.Err())
	}
	res.Duration, res.Output, res.Total = time.Since(start), out.Bytes(), out.total
	s.removeContainers(ctx, name)
	var exitErr *exec.ExitError
	switch {
	case werr == nil:
	case errors.As(werr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("run %s: %w", name, werr)
	}
	if !res.TimedOut && res.ExitCode == 125 && bytes.HasPrefix(res.Output, []byte("docker: ")) {
		return res, fmt.Errorf("run %s: %s", name, strings.TrimSpace(string(res.Output)))
	}
	return res, nil
}

// Start starts the long-lived subject container (an MCP server) detached.
// The image's entrypoint is kept; argv, when set, replaces its command.
// Workdir defaults to SandboxProject.
func (s *Sandbox) Start(ctx context.Context, argv []string, env map[string]string, workdir string) error {
	s.mu.Lock()
	if s.server != "" {
		s.mu.Unlock()
		return errors.New("start: subject already started")
	}
	s.server = s.prefix + "-srv"
	s.names = append(s.names, s.server)
	s.mu.Unlock()
	args, err := s.subjectArgs(s.server, workdir, env, true)
	if err != nil {
		return err
	}
	args = append(args, s.cfg.Image)
	args = append(args, argv...)
	if _, err := s.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("start %s: %w", s.server, err)
	}
	return nil
}

// Logs returns the stdout and stderr of the Start container, capped at
// 1 MiB, with the stream it ended on.
func (s *Sandbox) Logs(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	name := s.server
	s.mu.Unlock()
	if name == "" {
		return nil, errors.New("logs: no subject started")
	}
	res, err := s.cfg.Docker.Run(ctx, "logs", "--tail", "2000", name)
	if err != nil {
		return nil, fmt.Errorf("logs %s: %w", name, err)
	}
	out := append(append([]byte(nil), res.Stdout...), res.Stderr...)
	if len(out) > logsMax {
		out = out[len(out)-logsMax:]
	}
	return out, nil
}

// Running reports whether the Start container is still running, with its
// exit code when it is not.
func (s *Sandbox) Running(ctx context.Context) (bool, int, error) {
	s.mu.Lock()
	name := s.server
	s.mu.Unlock()
	if name == "" {
		return false, 0, errors.New("running: no subject started")
	}
	res, err := s.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}}", name)
	if err != nil {
		return false, 0, fmt.Errorf("inspect %s: %w", name, err)
	}
	f := strings.Fields(string(res.Stdout))
	if len(f) != 2 {
		return false, 0, fmt.Errorf("inspect %s: unexpected %q", name, res.Stdout)
	}
	code, err := strconv.Atoi(f[1])
	if err != nil {
		return false, 0, fmt.Errorf("inspect %s: exit code %q: %w", name, f[1], err)
	}
	return f[0] == "true", code, nil
}

// Attach runs `python3 -u -c script args...` from the helper image in the
// sandbox's network namespace, as nobody, with the subject's hardening and
// no home volume, and connects its stdin and stdout. It is how the scanner
// talks to an MCP server that only listens inside the sandbox. Close it.
func (s *Sandbox) Attach(ctx context.Context, script string, args ...string) (*Attached, error) {
	s.mu.Lock()
	s.attaches++
	name := fmt.Sprintf("%s-att-%d", s.prefix, s.attaches)
	s.names = append(s.names, name)
	s.mu.Unlock()
	run := []string{"run", "-i", "--rm", "--name", name}
	run = append(run, s.labelArgs()...)
	run = append(run, "--network", "container:"+s.holder, "--user", "65534:65534", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--pids-limit", "32", "--memory", "128m", "--cpus", "0.5",
		"--read-only", "--tmpfs", "/tmp:rw,nosuid,size=16m", "--log-driver", "none",
		"--entrypoint", "python3", s.cfg.HelperImage, "-u", "-c", script)
	run = append(run, args...)
	cmd := exec.Command(sbDockerBin(s.cfg.Docker), run...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("attach %s stdin: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("attach %s stdout: %w", name, err)
	}
	a := &Attached{Stdin: stdin, Stdout: stdout, name: name, cmd: cmd, d: s.cfg.Docker, stderr: &sbCappedBuffer{max: 64 << 10}}
	cmd.Stderr = a.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("attach %s: %w", name, err)
	}
	return a, nil
}

// Stderr returns what the attached process wrote to stderr so far, capped.
func (a *Attached) Stderr() string {
	return string(a.stderr.Bytes())
}

// Close ends the attached process and removes its container. It is safe to
// call more than once.
func (a *Attached) Close(ctx context.Context) error {
	a.once.Do(func() {
		var errs []error
		if err := a.Stdin.Close(); err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, os.ErrClosed) {
			errs = append(errs, fmt.Errorf("close stdin: %w", err))
		}
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupWait)
		defer cancel()
		if _, err := a.d.Run(cctx, "rm", "-f", a.name); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", a.name, err))
		}
		if err := a.cmd.Wait(); err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				errs = append(errs, fmt.Errorf("wait %s: %w", a.name, err))
			}
		}
		a.err = errors.Join(errs...)
	})
	return a.err
}

// Observe stops the watcher and returns what it saw. Call it once the
// subject has finished; later calls return the same observation.
func (s *Sandbox) Observe(ctx context.Context) (Observation, error) {
	s.mu.Lock()
	if s.observed != nil {
		o := *s.observed
		s.mu.Unlock()
		return o, nil
	}
	s.mu.Unlock()
	obs := Observation{Observed: true}
	if s.watch == nil {
		obs = Observation{Error: "the watcher never started"}
		s.setObserved(obs)
		return obs, nil
	}
	select {
	case <-s.watchDone:
		obs.Observed = false
		obs.Error = "the watcher stopped before the subject finished: " + sbOneLine(s.watchErr.String())
	default:
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), watchStopWait+cleanupWait)
	defer cancel()
	if _, err := s.cfg.Docker.Run(cctx, "kill", "--signal", "TERM", s.watcher); err != nil {
		s.logger().Warn("stop sandbox watcher", "container", s.watcher, "error", err.Error())
	}
	select {
	case <-s.watchDone:
	case <-time.After(watchStopWait):
		s.logger().Warn("sandbox watcher did not stop; removing it", "container", s.watcher)
	}
	s.removeContainers(cctx, s.watcher)
	if err := s.watch.Wait(); err != nil {
		s.logger().Debug("sandbox watcher ended", "container", s.watcher, "error", err.Error())
	}
	s.mu.Lock()
	lines := append([]string(nil), s.watchLines...)
	s.mu.Unlock()
	events, err := ParseWatchLines(lines)
	if err != nil {
		return Observation{}, fmt.Errorf("observe: %w", err)
	}
	obs.Events = events
	s.setObserved(obs)
	return obs, nil
}

// Close removes every container, the volume and the network the sandbox
// made. It is safe to call more than once and after a partial NewSandbox.
func (s *Sandbox) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupWait)
		defer cancel()
		var errs []error
		s.mu.Lock()
		names := append([]string(nil), s.names...)
		s.mu.Unlock()
		res, err := s.cfg.Docker.Run(cctx, "ps", "-aq", "--filter", labelAdmit+"="+s.prefix)
		if err == nil {
			names = append(names, strings.Fields(string(res.Stdout))...)
		}
		if len(names) > 0 {
			if _, err := s.cfg.Docker.Run(cctx, append([]string{"rm", "-f"}, names...)...); err != nil {
				errs = append(errs, fmt.Errorf("remove containers: %w", err))
			}
		}
		if s.watch != nil && s.watch.ProcessState == nil {
			<-s.watchDone
			if err := s.watch.Wait(); err != nil {
				s.logger().Debug("sandbox watcher ended", "error", err.Error())
			}
		}
		for _, rm := range [][]string{{"volume", "rm", "-f", s.volume}, {"network", "rm", s.network}} {
			res, err := s.cfg.Docker.Run(cctx, rm...)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s rm: %w", rm[0], err))
			case res.ExitCode != 0 && !sbNotFound(res.Stderr):
				errs = append(errs, fmt.Errorf("%s rm %s: %s", rm[0], rm[len(rm)-1], strings.TrimSpace(string(res.Stderr))))
			}
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

// ParseWatchLines decodes watcher output lines into events, dropping the
// ready and stats lines and blank lines. A line that is not JSON is an
// error: the watcher writes nothing else.
func ParseWatchLines(lines []string) ([]WatchEvent, error) {
	var out []WatchEvent
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev WatchEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("watch line %d: %w", i+1, err)
		}
		if ev.T == WatchReady || ev.T == WatchStats {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

// DynamicFindings turns an observation and the subject's output into
// findings: a canary file opened or read, a canary value (or its base64) in
// the output, and any network attempt block; a credential folder listed
// warns; an observation that did not happen blocks, because the run then
// proves nothing. Excerpts never carry a canary value.
func DynamicFindings(obs Observation, canaries []Canary, output []byte) []schemas.AdmitScanFinding {
	var out []schemas.AdmitScanFinding
	if !obs.Observed {
		out = append(out, schemas.AdmitScanFinding{Rule: RuleNotObserved, Severity: schemas.AdmitScanSeverityBlock, File: sbFileNetwork,
			Detail: "the sandbox watcher did not observe the run, so it proves nothing: " + obs.Error})
	}
	out = append(out, sbFileFindings(obs.Events, canaries)...)
	out = append(out, sbNetFindings(obs.Events, canaries)...)
	out = append(out, sbOutputFindings(output, canaries)...)
	return out
}

func newSandboxState(cfg SandboxConfig) (*Sandbox, error) {
	if cfg.Image == "" {
		return nil, errors.New("sandbox: no image")
	}
	if cfg.HelperImage == "" {
		cfg.HelperImage = DefaultHelperImage
	}
	if cfg.User == "" {
		cfg.User = DefaultSandboxUser
	}
	if cfg.PidsLimit <= 0 {
		cfg.PidsLimit = defaultPidsLimit
	}
	if cfg.MemoryMB <= 0 {
		cfg.MemoryMB = defaultMemoryMB
	}
	if cfg.CPUs == "" {
		cfg.CPUs = defaultCPUs
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "bench-admit-" + sbRandHex(4)
	}
	uid, gid, err := sbParseUser(cfg.User)
	if err != nil {
		return nil, err
	}
	canaries, err := newCanaries()
	if err != nil {
		return nil, err
	}
	p := cfg.Prefix
	return &Sandbox{cfg: cfg, uid: uid, gid: gid, prefix: p, network: p + "-net", volume: p + "-home",
		holder: p + "-hold", watcher: p + "-watch", canaries: canaries, watchDone: make(chan struct{})}, nil
}

func (s *Sandbox) logger() schemas.Logger {
	if s.cfg.Logger == nil {
		return schemas.NoopLogger{}
	}
	return s.cfg.Logger
}

func (s *Sandbox) labelArgs() []string {
	return append(utils.LabelArgs("", ""), "--label", labelAdmit+"="+s.prefix)
}

func (s *Sandbox) createNetwork(ctx context.Context) error {
	args := append([]string{"network", "create", "--internal", "--ipv6=false"}, s.labelArgs()...)
	_, err := s.cfg.Docker.MustRun(ctx, append(args, s.network)...)
	return err
}

func (s *Sandbox) createVolume(ctx context.Context) error {
	args := append([]string{"volume", "create"}, s.labelArgs()...)
	_, err := s.cfg.Docker.MustRun(ctx, append(args, s.volume)...)
	return err
}

// plant writes the canaries and the configured files into the home volume
// through a short-lived root container that may only change owners.
func (s *Sandbox) plant(ctx context.Context) error {
	tarball, err := BuildPlantTar(s.canaries, s.cfg.Files, s.uid, s.gid)
	if err != nil {
		return err
	}
	name := s.prefix + "-plant"
	s.mu.Lock()
	s.names = append(s.names, name)
	s.mu.Unlock()
	args := []string{"run", "-i", "--rm", "--name", name}
	args = append(args, s.labelArgs()...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "FOWNER",
		"--security-opt", "no-new-privileges", "--read-only", "--pids-limit", "16", "--memory", "256m", "--log-driver", "none",
		"-v", s.volume+":/h", "--entrypoint", "python3", s.cfg.HelperImage, "-c", sandboxPlantScript,
		strconv.Itoa(s.uid), strconv.Itoa(s.gid))
	cmd := exec.CommandContext(ctx, sbDockerBin(s.cfg.Docker), args...)
	cmd.Stdin = bytes.NewReader(tarball)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, sbOneLine(string(out)))
	}
	if !strings.Contains(string(out), "ok") {
		return fmt.Errorf("planting printed %q", sbOneLine(string(out)))
	}
	return nil
}

func (s *Sandbox) startHolder(ctx context.Context) error {
	s.mu.Lock()
	s.names = append(s.names, s.holder)
	s.mu.Unlock()
	args := append([]string{"run", "-d", "--name", s.holder}, s.labelArgs()...)
	args = append(args, "--network", s.network, "--dns-search", ".", "--sysctl", "net.ipv6.conf.all.disable_ipv6=1",
		"--user", "65534:65534", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only",
		"--pids-limit", "8", "--memory", "32m", "--log-driver", "none", "--entrypoint", "sleep", s.cfg.HelperImage, "infinity")
	_, err := s.cfg.Docker.MustRun(ctx, args...)
	return err
}

// addRoute runs the route script with only CAP_NET_ADMIN in the holder's
// network namespace.
func (s *Sandbox) addRoute(ctx context.Context) error {
	name := s.prefix + "-route"
	s.mu.Lock()
	s.names = append(s.names, name)
	s.mu.Unlock()
	args := append([]string{"run", "--rm", "--name", name}, s.labelArgs()...)
	args = append(args, "--network", "container:"+s.holder, "--user", "0", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN",
		"--security-opt", "no-new-privileges", "--read-only", "--pids-limit", "8", "--memory", "64m",
		"--entrypoint", "python3", s.cfg.HelperImage, "-c", sandboxRouteScript, "eth0")
	_, err := s.cfg.Docker.MustRun(ctx, args...)
	return err
}

// startWatcher starts the watcher in the holder's namespace with the home
// volume read-only, and waits for its ready line.
func (s *Sandbox) startWatcher(ctx context.Context) error {
	cfg, err := json.Marshal(sbWatchConfig(s.canaries))
	if err != nil {
		return fmt.Errorf("encode watch config: %w", err)
	}
	s.mu.Lock()
	s.names = append(s.names, s.watcher)
	s.mu.Unlock()
	args := append([]string{"run", "--name", s.watcher}, s.labelArgs()...)
	args = append(args, "--network", "container:"+s.holder, "--user", "0", "--cap-drop", "ALL",
		"--cap-add", "NET_RAW", "--cap-add", "DAC_READ_SEARCH", "--security-opt", "no-new-privileges", "--read-only",
		"--pids-limit", "16", "--memory", "128m", "--log-driver", "none", "-v", s.volume+":/w:ro",
		"--entrypoint", "python3", s.cfg.HelperImage, "-u", "-c", sandboxWatchScript, string(cfg))
	cmd := exec.Command(sbDockerBin(s.cfg.Docker), args...)
	cmd.Stderr = &s.watchErr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("watcher stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("watcher: %w", err)
	}
	s.watch = cmd
	ready := make(chan WatchEvent, 1)
	go s.readWatch(stdout, ready)
	select {
	case ev := <-ready:
		if ev.T != WatchReady {
			return fmt.Errorf("watcher failed: %s", ev.Error)
		}
		if ev.Files != len(s.canaries) || ev.Dirs != len(credentialDirs) {
			return fmt.Errorf("watcher watches %d of %d canary files and %d of %d folders", ev.Files, len(s.canaries), ev.Dirs, len(credentialDirs))
		}
		return nil
	case <-s.watchDone:
		return fmt.Errorf("watcher exited before it was ready: %s", sbOneLine(s.watchErr.String()))
	case <-time.After(watchReadyWait):
		return fmt.Errorf("watcher not ready after %s", watchReadyWait)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// readWatch hands the first line (ready or error) to ready and keeps every
// later line, capped, until the watcher's stdout ends.
func (s *Sandbox) readWatch(r io.Reader, ready chan<- WatchEvent) {
	defer close(s.watchDone)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			var ev WatchEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				ev = WatchEvent{T: WatchError, Error: "unreadable first line: " + sbOneLine(line)}
			}
			ready <- ev
			continue
		}
		s.mu.Lock()
		if s.watchBytes+len(line) <= watchLinesMax {
			s.watchLines = append(s.watchLines, line)
			s.watchBytes += len(line)
		}
		s.mu.Unlock()
	}
}

func (s *Sandbox) setObserved(o Observation) {
	s.mu.Lock()
	s.observed = &o
	s.mu.Unlock()
}

// subjectArgs are the docker run flags of a subject container: the agent
// hardening, the holder's network namespace and the home volume.
func (s *Sandbox) subjectArgs(name, workdir string, env map[string]string, detach bool) ([]string, error) {
	if workdir == "" {
		workdir = SandboxProject
	}
	args := []string{"run"}
	if detach {
		args = append(args, "-d")
	}
	args = append(args, "--name", name)
	args = append(args, s.labelArgs()...)
	args = append(args, "--network", "container:"+s.holder, "--user", s.cfg.User, "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--pids-limit", strconv.Itoa(s.cfg.PidsLimit),
		"--memory", strconv.Itoa(s.cfg.MemoryMB)+"m", "--cpus", s.cfg.CPUs, "--read-only",
		"--tmpfs", "/tmp:rw,exec,nosuid,size=64m", "-v", s.volume+":"+SandboxHome, "-e", "HOME="+SandboxHome,
		"-w", workdir, "--init", "--log-driver", "json-file", "--log-opt", "max-size=1m", "--log-opt", "max-file=1")
	keys := make([]string, 0, len(env))
	for k := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") {
			return nil, fmt.Errorf("environment variable name %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	return args, nil
}

// removeContainers force-removes containers, logging failures.
func (s *Sandbox) removeContainers(ctx context.Context, names ...string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupWait)
	defer cancel()
	if _, err := s.cfg.Docker.Run(cctx, append([]string{"rm", "-f"}, names...)...); err != nil {
		s.logger().Warn("remove sandbox containers", "names", strings.Join(names, ","), "error", err.Error())
	}
}

// BuildPlantTar builds the tar the planting container extracts into the
// home volume: the credential folders, the canary files (0600), the parent
// folders of every planted file, and the planted files, all owned by
// uid:gid. Paths must be relative, clean and unique.
func BuildPlantTar(canaries []Canary, files []PlantedFile, uid, gid int) ([]byte, error) {
	type entry struct {
		name string
		mode int64
		data []byte
	}
	var plain []entry
	dirs := map[string]int64{".ssh": 0o700, ".aws": 0o700, ".config": 0o755, ".config/gh": 0o700, "project": 0o755}
	for _, c := range canaries {
		rel, err := sbHomeRel(c.Path)
		if err != nil {
			return nil, fmt.Errorf("canary %s: %w", c.ID, err)
		}
		plain = append(plain, entry{rel, 0o600, sbCanaryFile(c)})
	}
	for _, f := range files {
		rel := strings.TrimPrefix(f.Path, "./")
		if rel == "" || path.IsAbs(rel) || path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil, fmt.Errorf("planted file path %q is not a clean relative path", f.Path)
		}
		mode := f.Mode & 0o777
		if mode == 0 {
			mode = 0o644
		}
		plain = append(plain, entry{rel, mode, f.Data})
	}
	seen := map[string]bool{}
	for _, e := range plain {
		if seen[e.name] {
			return nil, fmt.Errorf("planted file %q given twice", e.name)
		}
		seen[e.name] = true
		for d := path.Dir(e.name); d != "."; d = path.Dir(d) {
			if _, ok := dirs[d]; !ok {
				dirs[d] = 0o755
			}
		}
	}
	names := make([]string, 0, len(dirs))
	for d := range dirs {
		if seen[d] {
			return nil, fmt.Errorf("planted path %q is both a file and a folder", d)
		}
		names = append(names, d)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	mtime := time.Unix(0, 0)
	for _, d := range names {
		h := &tar.Header{Typeflag: tar.TypeDir, Name: d + "/", Mode: dirs[d], Uid: uid, Gid: gid, ModTime: mtime, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return nil, fmt.Errorf("tar folder %s: %w", d, err)
		}
	}
	for _, e := range plain {
		h := &tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: e.mode, Size: int64(len(e.data)), Uid: uid, Gid: gid, ModTime: mtime, Format: tar.FormatPAX}
		if err := tw.WriteHeader(h); err != nil {
			return nil, fmt.Errorf("tar %s: %w", e.name, err)
		}
		if _, err := tw.Write(e.data); err != nil {
			return nil, fmt.Errorf("tar %s: %w", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("close tar: %w", err)
	}
	return buf.Bytes(), nil
}

// newCanaries makes the five canary credentials with fresh random values.
func newCanaries() ([]Canary, error) {
	vals := make([]string, 5)
	for i := range vals {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("canary value: %w", err)
		}
		vals[i] = hex.EncodeToString(b)
	}
	return []Canary{
		{ID: "ssh_key", Path: SandboxHome + "/.ssh/id_ed25519", Value: "b3BlbnNzaC1rZXktdjE" + vals[0]},
		{ID: "aws", Path: SandboxHome + "/.aws/credentials", Value: "bfcanary" + vals[1]},
		{ID: "gh", Path: SandboxHome + "/.config/gh/hosts.yml", Value: "ghp_bfcanary" + vals[2]},
		{ID: "netrc", Path: SandboxHome + "/.netrc", Value: "bfnetrc" + vals[3]},
		{ID: "dotenv", Path: SandboxProject + "/.env", Value: "sk-bfcanary-" + vals[4]},
	}, nil
}

// sbCanaryFile is the file content that carries a canary value, shaped like
// the real credential file so a script that parses it finds the value.
func sbCanaryFile(c Canary) []byte {
	switch c.ID {
	case "ssh_key":
		return []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n" + c.Value + "\n-----END OPENSSH PRIVATE KEY-----\n")
	case "aws":
		return []byte("[default]\naws_access_key_id = AKIABFCANARY0000000\naws_secret_access_key = " + c.Value + "\n")
	case "gh":
		return []byte("github.com:\n    user: bench-canary\n    oauth_token: " + c.Value + "\n    git_protocol: https\n")
	case "netrc":
		return []byte("machine api.github.com\n  login bench-canary\n  password " + c.Value + "\n")
	default:
		return []byte("API_KEY=" + c.Value + "\nDATABASE_URL=postgres://app:" + c.Value + "@db:5432/app\n")
	}
}

// sbWatchConfig is the watcher's argv[1].
func sbWatchConfig(canaries []Canary) map[string]any {
	files := make([]string, 0, len(canaries))
	for _, c := range canaries {
		if rel, err := sbHomeRel(c.Path); err == nil {
			files = append(files, rel)
		}
	}
	return map[string]any{"root": "/w", "home": SandboxHome, "files": files, "dirs": credentialDirs, "max": 2000}
}

// sbHomeRel returns a container path relative to the home folder.
func sbHomeRel(p string) (string, error) {
	rel, ok := strings.CutPrefix(p, SandboxHome+"/")
	if !ok || rel == "" || path.Clean(rel) != rel || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("path %q is not under %s", p, SandboxHome)
	}
	return rel, nil
}

// sbParseUser reads "uid:gid" (numeric only: the planted files need the
// owner before any image is consulted).
func sbParseUser(u string) (int, int, error) {
	a, b, ok := strings.Cut(u, ":")
	uid, err1 := strconv.Atoi(a)
	gid, err2 := strconv.Atoi(b)
	if !ok || err1 != nil || err2 != nil || uid < 0 || gid < 0 {
		return 0, 0, fmt.Errorf("sandbox user %q: want numeric uid:gid", u)
	}
	if uid == 0 {
		return 0, 0, errors.New("sandbox user: the subject never runs as root")
	}
	return uid, gid, nil
}

// sbFileFindings reports canary file opens (one finding per canary) and
// credential folder listings.
func sbFileFindings(events []WatchEvent, canaries []Canary) []schemas.AdmitScanFinding {
	var out []schemas.AdmitScanFinding
	ops := map[string][]string{}
	var order []string
	listed := map[string]bool{}
	for _, ev := range events {
		switch ev.T {
		case WatchOpen, WatchAccess:
			if _, ok := ops[ev.Path]; !ok {
				order = append(order, ev.Path)
			}
			if !sbContains(ops[ev.Path], ev.T) {
				ops[ev.Path] = append(ops[ev.Path], ev.T)
			}
		case WatchDirOpen:
			if !listed[ev.Path] {
				listed[ev.Path] = true
				out = append(out, schemas.AdmitScanFinding{Rule: RuleCredentialDirListed, Severity: schemas.AdmitScanSeverityWarn, File: ev.Path,
					Excerpt: "dir_open " + ev.Path, Detail: "the credential folder " + ev.Path + " was opened as a folder (listed)"})
			}
		}
	}
	for _, p := range order {
		id := "unknown"
		for _, c := range canaries {
			if c.Path == p {
				id = c.ID
			}
		}
		out = append(out, schemas.AdmitScanFinding{Rule: RuleCanaryRead, Severity: schemas.AdmitScanSeverityBlock, File: p,
			Excerpt: strings.Join(ops[p], ", ") + " " + p,
			Detail:  fmt.Sprintf("canary %s (%s) was opened by the subject; a skill's setup or an MCP server has no reason to touch another account's credentials", id, p)})
	}
	return out
}

// sbNetFindings reports one finding per distinct network attempt. An ARP
// request for one of Docker's upstream DNS servers is the resolver
// forwarding a lookup, already reported as the DNS query, unless no query
// was seen.
func sbNetFindings(events []WatchEvent, canaries []Canary) []schemas.AdmitScanFinding {
	var out []schemas.AdmitScanFinding
	sawDNS := false
	for _, ev := range events {
		sawDNS = sawDNS || ev.T == WatchDNS
	}
	seen := map[string]bool{}
	for _, ev := range events {
		var key, detail string
		switch ev.T {
		case WatchDNS:
			key, detail = "dns query "+ev.Name, "DNS query for "+ev.Name
			for _, c := range canaries {
				if strings.Contains(strings.ToLower(ev.Name), strings.ToLower(c.Value)) {
					detail += fmt.Sprintf("; the name carries canary %s", c.ID)
				}
			}
		case WatchSYN:
			key, detail = fmt.Sprintf("tcp connect %s:%d", ev.Dst, ev.Port), "TCP connection attempt"
		case WatchUDP:
			key, detail = fmt.Sprintf("udp send %s:%d", ev.Dst, ev.Port), "UDP datagram"
		case WatchARP:
			if ev.Resolver && sawDNS {
				continue
			}
			key, detail = "connect "+ev.Dst, "connection attempt to "+ev.Dst+" (seen as an ARP request; the sandbox has no route out, so it failed)"
			if ev.Resolver {
				detail += "; the address is an upstream DNS server of the Docker host"
			}
		default:
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, schemas.AdmitScanFinding{Rule: RuleNetworkAttempt, Severity: schemas.AdmitScanSeverityBlock, File: sbFileNetwork,
			Excerpt: sbRedact(key, canaries), Detail: sbRedact(detail, canaries) + "; the subject tried to reach the network"})
	}
	return out
}

// sbOutputFindings reports each canary whose value, or its base64, appears
// in the output, with the line it appeared on, redacted.
func sbOutputFindings(output []byte, canaries []Canary) []schemas.AdmitScanFinding {
	var out []schemas.AdmitScanFinding
	text := string(output)
	for _, c := range canaries {
		if c.Value == "" {
			continue
		}
		enc := base64.StdEncoding.EncodeToString([]byte(c.Value))
		idx, how := strings.Index(text, c.Value), "its value"
		if idx < 0 {
			idx, how = strings.Index(text, enc), "its base64"
		}
		if idx < 0 {
			continue
		}
		start := strings.LastIndexByte(text[:idx], '\n') + 1
		end := strings.IndexByte(text[idx:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += idx
		}
		out = append(out, schemas.AdmitScanFinding{Rule: RuleCanaryInOutput, Severity: schemas.AdmitScanSeverityBlock, File: sbFileOutput,
			Excerpt: sbClip(Visible(sbRedact(text[start:end], canaries)), sbExcerptMax),
			Detail:  fmt.Sprintf("canary %s (%s) appeared in the output as %s", c.ID, c.Path, how)})
	}
	return out
}

// sbRedact replaces every canary value and its base64 with [CANARY:<id>].
func sbRedact(s string, canaries []Canary) string {
	for _, c := range canaries {
		if c.Value == "" {
			continue
		}
		r := fmt.Sprintf(sbCanaryRedaction, c.ID)
		s = strings.ReplaceAll(s, c.Value, r)
		s = strings.ReplaceAll(s, base64.StdEncoding.EncodeToString([]byte(c.Value)), r)
		s = strings.ReplaceAll(s, strings.ToLower(c.Value), r)
	}
	return s
}

func sbClip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

func sbContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func sbRandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func sbDockerBin(d utils.Docker) string {
	if d.Binary == "" {
		return "docker"
	}
	return d.Binary
}

// sbOneLine squeezes text into one line of at most 300 bytes for an error.
func sbOneLine(s string) string {
	return sbClip(strings.Join(strings.Fields(s), " "), 300)
}

func sbNotFound(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	return strings.Contains(s, "no such") || strings.Contains(s, "not found")
}

// sbCappedBuffer keeps the first max bytes written and counts the rest.
type sbCappedBuffer struct {
	mu    sync.Mutex
	max   int
	buf   bytes.Buffer
	total int64
}

// Write implements io.Writer; it never fails, so the writer never blocks
// the process it reads.
func (c *sbCappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.total += int64(len(p))
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

// Bytes returns a copy of what was kept.
func (c *sbCappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}
