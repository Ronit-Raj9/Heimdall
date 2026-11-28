package main

// Build sessions (G3 B3): every image an organization builds from its own
// Dockerfile on the shared worker runs in a session of its own.
//
//	bifrost-bench runner build-session -org <id> -runner <name> -lock-dir <dir> -docker <bin> -- buildx build --load ...
//
// The shared runner's Docker policy runs every org build through this
// command (utils.DockerPolicy.Session). One session:
//
//  1. takes the org's build lock (one build at a time per org; other orgs
//     build in parallel in sessions of their own);
//  2. starts an egress guard on the build network: a small container with
//     NET_ADMIN only that installs the build's egress rules
//     (deploy/hosted/build-egress.sh: no cloud metadata, no private or
//     link-local range, no other compose service, no IPv6; the read-only
//     registry and the public internet stay reachable);
//  3. starts a fresh rootless BuildKit inside the guard's network
//     namespace, with the org's own build cache volume
//     (heimdall-buildcache-<org>), so no build shares a daemon, a process
//     table or a cache with another org's build;
//  4. runs the build against it with the registry's read-only pull
//     credentials in a docker config of its own;
//  5. removes the BuildKit and the guard, whatever the outcome. A session
//     killed before it could clean up is removed when the runner next
//     starts (sweepBuildSessions).

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
)

// Build session environment (deploy/hosted/docker-compose.yml, shared-runner).
const (
	// buildkitImageEnv is the rootless BuildKit image (pinned by digest)
	// every session starts. Set: org builds run in sessions.
	buildkitImageEnv = "HEIMDALL_BUILDKIT_IMAGE"
	// buildGuardImageEnv is the image of the egress guard (the runner image,
	// which carries iptables and deploy/hosted/build-egress.sh).
	buildGuardImageEnv = "HEIMDALL_BUILD_GUARD_IMAGE"
	// buildNetworkEnv is the build network: the read-only registry, the
	// shared runner and the sessions' guards, nothing else.
	buildNetworkEnv = "HEIMDALL_BUILD_NETWORK"
	// buildkitAppArmorEnv is the AppArmor profile of each session's BuildKit
	// (heimdall-buildkitd on hosts that restrict user namespaces).
	buildkitAppArmorEnv = "HEIMDALL_BUILDKIT_APPARMOR"
	// buildkitConfigEnv is the Docker host path of buildkitd.toml.
	buildkitConfigEnv = "HEIMDALL_BUILDKIT_CONFIG"
	// registryAuthEnv is the file holding the registry's read-only pull
	// credentials (user:password).
	registryAuthEnv = "HEIMDALL_SHARED_REGISTRY_AUTH"
	// orgBuildsEnv is how many image builds of one org may run at once
	// (default 2, at most 8). Each build slot has its own lock file and its
	// own cache volume, since two BuildKit daemons must not share a state
	// folder. 1 restores one build at a time on the org's single cache.
	orgBuildsEnv = "HEIMDALL_ORG_BUILDS"
	// registryManifestEnv is the registry manifest (image and digest per
	// line) registry-sync.sh writes; org builds use base images by these
	// digests only.
	registryManifestEnv = "HEIMDALL_SHARED_REGISTRY_MANIFEST"

	defaultBuildNetwork = "heimdall-buildnet"
	buildGuardEntry     = "/usr/local/bin/heimdall-build-egress"
	buildGuardReady     = "heimdall build egress ready"
	// labelBuildSession marks a session's containers (value: session id);
	// labelBuildRunner names the shared runner that started it.
	labelBuildSession = "heimdall.build-session"
	labelBuildRunner  = "heimdall.build-runner"
)

// buildSessionTimeouts bound the session's setup steps.
var (
	buildGuardTimeout   = 30 * time.Second
	buildkitTimeout     = 60 * time.Second
	buildCleanupTimeout = 30 * time.Second
	buildPoll           = 200 * time.Millisecond
)

// buildSessionConfig is what a session needs besides the build itself.
type buildSessionConfig struct {
	Org, Runner, LockDir string
	Slots                int // build slots of the org; <= 1 is one at a time
	Docker               string
	Network              string
	BuildkitImage        string
	GuardImage           string
	AppArmor             string
	BuildkitConfig       string // host path, "" for BuildKit's defaults
	Registry             string // host:port, "" for none
	Credentials          *utils.RegistryCredentials
	Stdin                io.Reader
	Stdout, Stderr       io.Writer
	newID                func() string
	afterReady           func() // tests: called once BuildKit answers
}

// buildSessionFromEnv fills the session configuration from the runner's
// environment.
func buildSessionFromEnv(c *buildSessionConfig) error {
	c.BuildkitImage = strings.TrimSpace(os.Getenv(buildkitImageEnv))
	c.GuardImage = strings.TrimSpace(os.Getenv(buildGuardImageEnv))
	c.Network = strings.TrimSpace(os.Getenv(buildNetworkEnv))
	if c.Network == "" {
		c.Network = defaultBuildNetwork
	}
	c.AppArmor = strings.TrimSpace(os.Getenv(buildkitAppArmorEnv))
	c.BuildkitConfig = strings.TrimSpace(os.Getenv(buildkitConfigEnv))
	c.Registry = strings.TrimSpace(os.Getenv(sharedRegistryEnv))
	c.Slots = orgBuildSlots(os.Getenv(orgBuildsEnv))
	if c.BuildkitImage == "" || c.GuardImage == "" {
		return fmt.Errorf("%s and %s must be set for build sessions", buildkitImageEnv, buildGuardImageEnv)
	}
	if path := strings.TrimSpace(os.Getenv(registryAuthEnv)); path != "" && c.Registry != "" {
		cred, err := utils.ReadRegistryCredentials(path)
		if err != nil {
			return err
		}
		c.Credentials = cred
	}
	return nil
}

func runBuildSession(args []string) int {
	c := &buildSessionConfig{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	fs := flag.NewFlagSet("runner build-session", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.Org, "org", "", "organization id")
	fs.StringVar(&c.Runner, "runner", "", "name of the shared runner that starts the session")
	fs.StringVar(&c.LockDir, "lock-dir", "", "folder of the per-org build locks")
	fs.StringVar(&c.Docker, "docker", "docker", "docker CLI")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "runner build-session: %v\n", err)
		return exitUsage
	}
	build := fs.Args()
	switch {
	case c.Org == "" || c.Runner == "" || c.LockDir == "":
		fmt.Fprintln(os.Stderr, "runner build-session: -org, -runner and -lock-dir are required")
		return exitUsage
	case tenancy.ValidOrgID(c.Org) != nil:
		fmt.Fprintf(os.Stderr, "runner build-session: invalid org id %q\n", c.Org)
		return exitUsage
	case len(build) < 2 || build[0] != "buildx" || build[1] != "build":
		fmt.Fprintln(os.Stderr, "runner build-session: the command after -- must be a docker buildx build")
		return exitUsage
	}
	if err := buildSessionFromEnv(c); err != nil {
		return fail("runner build-session: %v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code, err := c.run(ctx, build)
	if err != nil {
		fmt.Fprintf(os.Stderr, "image build session for %s: %v\n", c.Org, err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

// buildSessionID is a random session id (12 hex characters).
func buildSessionID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// sessionNames are the docker object names of one session.
type sessionNames struct {
	id, guard, buildkit, builder, volume string
	// host is the guard's network alias, the name buildx dials. The
	// container names carry the org id and pass DNS's 63 character label
	// limit for a real org id (org_ plus 26), which Docker's resolver then
	// refuses ("Message too large").
	host string
}

func newSessionNames(org, id string) sessionNames {
	return newSlotSessionNames(org, id, 0)
}

// newSlotSessionNames: build slot 0 keeps the names of one build at a
// time (so its cache survives the change); slot k > 0 has its own cache
// volume heimdall-buildcache-<org>-<k>, still only that org's.
func newSlotSessionNames(org, id string, slot int) sessionNames {
	base := "heimdall-build-" + org + "-" + id
	vol := "heimdall-buildcache-" + org
	if slot > 0 {
		vol += "-" + strconv.Itoa(slot)
	}
	return sessionNames{id: id, guard: base + "-guard", buildkit: base + "-buildkit", builder: base, host: "hb-build-" + id,
		volume: vol}
}

const (
	defaultOrgBuilds = 2
	maxOrgBuilds     = 8
)

// orgBuildSlots reads HEIMDALL_ORG_BUILDS: empty or invalid gives the
// default, and the value is kept within 1 and maxOrgBuilds.
func orgBuildSlots(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	switch {
	case err != nil:
		return defaultOrgBuilds
	case n < 1:
		return 1
	case n > maxOrgBuilds:
		return maxOrgBuilds
	}
	return n
}

// orgBuildLocks are the lock files of an org's build slots: slot 0 is
// <org>.lock as before, slot k is <org>.<k>.lock.
func orgBuildLocks(dir, org string, slots int) []string {
	if slots < 1 {
		slots = 1
	}
	paths := []string{filepath.Join(dir, org+".lock")}
	for k := 1; k < slots; k++ {
		paths = append(paths, filepath.Join(dir, org+"."+strconv.Itoa(k)+".lock"))
	}
	return paths
}

// run is the session: lock, guard, BuildKit, build, cleanup. It returns
// the build's exit code.
func (c *buildSessionConfig) run(ctx context.Context, build []string) (int, error) {
	unlock, slot, err := lockOrgBuildSlot(ctx, orgBuildLocks(c.LockDir, c.Org, c.Slots))
	if err != nil {
		return 1, err
	}
	defer unlock()
	newID := c.newID
	if newID == nil {
		newID = buildSessionID
	}
	n := newSlotSessionNames(c.Org, newID(), slot)
	d := utils.Docker{Binary: c.Docker}
	cfgDir, err := os.MkdirTemp("", "heimdall-build-")
	if err != nil {
		return 1, err
	}
	env := append(os.Environ(), "DOCKER_CONFIG="+cfgDir, "BUILDX_BUILDER="+n.builder, "BUILDX_NO_DEFAULT_ATTESTATIONS=1")
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), buildCleanupTimeout)
		defer cancel()
		_, _ = d.RunEnv(cctx, env, "buildx", "rm", "-f", n.builder)
		_, _ = d.Run(cctx, "rm", "-f", n.buildkit)
		_, _ = d.Run(cctx, "rm", "-f", n.guard)
		os.RemoveAll(cfgDir)
	}()
	if err := writeSessionDockerConfig(cfgDir, c.Registry, c.Credentials); err != nil {
		return 1, err
	}
	if err := c.start(ctx, d, env, n); err != nil {
		return 1, err
	}
	if c.afterReady != nil {
		c.afterReady()
	}
	cmd := exec.CommandContext(ctx, c.Docker, build...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr) && ctx.Err() == nil:
		return exitErr.ExitCode(), nil
	}
	return 1, fmt.Errorf("build: %w", err)
}

// start brings up the session's guard and BuildKit and its buildx builder.
func (c *buildSessionConfig) start(ctx context.Context, d utils.Docker, env []string, n sessionNames) error {
	labels := []string{"--label", "heimdall=1", "--label", "heimdall.org=" + c.Org,
		"--label", labelBuildSession + "=" + n.id, "--label", labelBuildRunner + "=" + c.Runner}
	if _, err := d.MustRun(ctx, append(append([]string{"volume", "create"}, labels[:4]...), n.volume)...); err != nil {
		return fmt.Errorf("build cache volume: %w", err)
	}
	guard := append([]string{"run", "-d", "--rm", "--name", n.guard}, labels...)
	guard = append(guard, "--network", c.Network, "--network-alias", n.host, "--user", "0:0", "--cap-drop", "ALL", "--cap-add", "NET_ADMIN",
		"--security-opt", "no-new-privileges:true", "--read-only", "--tmpfs", "/run", "--tmpfs", "/tmp",
		"--memory", "64m", "--pids-limit", "64", "--entrypoint", buildGuardEntry, c.GuardImage)
	if _, err := d.MustRun(ctx, guard...); err != nil {
		return fmt.Errorf("start the build's egress guard: %w", err)
	}
	if err := waitGuard(ctx, d, n.guard); err != nil {
		return err
	}
	apparmor := c.AppArmor
	if apparmor == "" {
		apparmor = "unconfined"
	}
	bk := append([]string{"run", "-d", "--rm", "--name", n.buildkit}, labels...)
	bk = append(bk, "--network", "container:"+n.guard,
		"--security-opt", "seccomp=unconfined", "--security-opt", "apparmor="+apparmor, "--security-opt", "systempaths=unconfined",
		"-v", n.volume+":/home/user/.local/share/buildkit")
	if c.BuildkitConfig != "" {
		bk = append(bk, "-v", c.BuildkitConfig+":/home/user/.config/buildkit/buildkitd.toml:ro")
	}
	bk = append(bk, c.BuildkitImage, "--addr", "tcp://0.0.0.0:1234", "--oci-worker-no-process-sandbox")
	if _, err := d.MustRun(ctx, bk...); err != nil {
		return fmt.Errorf("start the build's BuildKit: %w", err)
	}
	if _, err := d.MustRunEnv(ctx, env, "buildx", "create", "--name", n.builder, "--driver", "remote", "tcp://"+n.host+":1234"); err != nil {
		return fmt.Errorf("connect to the build's BuildKit: %w", err)
	}
	deadline := time.Now().Add(buildkitTimeout)
	for {
		res, err := d.RunEnv(ctx, env, "buildx", "inspect", "--bootstrap", n.builder)
		if err == nil && res.ExitCode == 0 {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			msg := ""
			if res != nil {
				msg = strings.TrimSpace(string(res.Stderr))
			}
			logs, _ := d.Run(ctx, "logs", "--tail", "20", n.buildkit)
			if logs != nil {
				msg += "\n" + strings.TrimSpace(string(logs.Stdout)+string(logs.Stderr))
			}
			return fmt.Errorf("the build's BuildKit did not answer within %s: %s", buildkitTimeout, strings.TrimSpace(msg))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(buildPoll):
		}
	}
}

// waitGuard waits until the guard printed that its rules are in place.
func waitGuard(ctx context.Context, d utils.Docker, name string) error {
	deadline := time.Now().Add(buildGuardTimeout)
	for {
		res, err := d.Run(ctx, "logs", name)
		out := ""
		if res != nil {
			out = string(res.Stdout) + string(res.Stderr)
		}
		if err == nil && strings.Contains(out, buildGuardReady) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		state, _ := d.Run(ctx, "inspect", "--format", "{{.State.Running}}", name)
		stopped := state != nil && state.ExitCode == 0 && strings.TrimSpace(string(state.Stdout)) == "false"
		if stopped || (state != nil && state.ExitCode != 0) || time.Now().After(deadline) {
			return fmt.Errorf("the build's egress rules were not installed, so the build does not run: %s", strings.TrimSpace(out))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(buildPoll):
		}
	}
}

// writeSessionDockerConfig writes the session's docker config: only the
// registry's read-only pull credentials, readable by this process only.
func writeSessionDockerConfig(dir, registry string, cred *utils.RegistryCredentials) error {
	cfg := map[string]any{}
	if registry != "" && cred != nil {
		cfg["auths"] = map[string]any{registry: map[string]string{
			"auth": base64.StdEncoding.EncodeToString([]byte(cred.User + ":" + cred.Password)),
		}}
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600)
}

// sweepBuildSessions removes the containers of sessions this runner
// started that are still there (a session killed before its cleanup). It
// runs when the runner starts, before any session of its own.
func sweepBuildSessions(ctx context.Context, d utils.Docker, runner string) (int, error) {
	res, err := d.MustRun(ctx, "ps", "-aq", "--filter", "label="+labelBuildSession, "--filter", "label="+labelBuildRunner+"="+runner)
	if err != nil {
		return 0, err
	}
	ids := strings.Fields(string(res.Stdout))
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := d.MustRun(ctx, append([]string{"rm", "-f"}, ids...)...); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// buildSessionArgv is the Session prefix of an org's Docker policy.
func buildSessionArgv(exe, org, runner, lockDir string) []string {
	return []string{exe, "runner", "build-session", "-org", org, "-runner", runner, "-lock-dir", lockDir}
}

// checkBuildSessions verifies at runner start that sessions can run: the
// build network exists and the guard image is on the host.
func checkBuildSessions(ctx context.Context, d utils.Docker) error {
	var c buildSessionConfig
	if err := buildSessionFromEnv(&c); err != nil {
		return err
	}
	if res, err := d.Run(ctx, "network", "inspect", "--format", "{{.Name}}", c.Network); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("build network %s not found (%s): start the stack with deploy/hosted/upgrade.sh", c.Network, buildNetworkEnv)
	}
	if res, err := d.Run(ctx, "image", "inspect", "--format", "{{.Id}}", c.GuardImage); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("egress guard image %s not on the Docker host (%s)", c.GuardImage, buildGuardImageEnv)
	}
	return nil
}
