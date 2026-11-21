package main

// Shared runner mode (web parity lane L03): `runner start -shared` serves
// every org the hosted server assigns to the shared pool. It fetches the
// assignments with HEIMDALL_SHARED_RUNNER_SECRET (they carry no tokens),
// claims one runner token per org (pool "shared"), and runs one worker loop
// per org. Like any runner it replans every leased experiment.
//
// Isolation between orgs on the one host (PROD2 N2 and P0 isolation):
//
//   - each org's worker has its own Docker policy (utils/dockerpolicy.go):
//     every container starts under the runner's runtime (gVisor runsc by
//     default when the host has it), org-written Dockerfiles build only on
//     the isolated BuildKit builder under the org's image namespace
//     (org-<id>/...), and pulls are limited to the org's approved images;
//   - each org's agents run on their own internal Docker network
//     (heimdall-evalnet-<org>), which only the gateway joins, so one org's
//     containers cannot reach another's;
//   - the runner mounts no server data: each org's project is downloaded
//     with that org's own runner token before every lease
//     (GET /workers/project) into a folder of its own;
//   - hosted CLI jobs are refused here (they would run outside the policy);
//     they run on an org's own workers.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

const sharedRunnerUsage = `usage:
  bifrost-bench runner start -shared -server <url> -secret-file <path> [-runtime auto|runc|runsc] [-slots n] [-poll 30s] [-name prefix]
      [-builder name | -buildkit tcp://host:port] [-gateway-container name] [-projects-dir path] [-config deploy/bench.yaml] [-debug]`

// Shared runner environment (deploy/hosted/docker-compose.yml).
const (
	// sharedMirrorEnv names the folder (bind-mounted at the same path on the
	// host) where the shared runner keeps each org's downloaded project.
	sharedMirrorEnv = "HEIMDALL_SHARED_PROJECTS_MIRROR"
	// sharedBuilderEnv names an existing buildx builder for org images.
	sharedBuilderEnv = "HEIMDALL_SHARED_BUILDER"
	// sharedBuildkitEnv is the address of the isolated BuildKit daemon
	// (tcp://buildkitd:1234); the runner creates a remote buildx builder on it.
	sharedBuildkitEnv = "HEIMDALL_SHARED_BUILDKIT"
	// sharedGatewayEnv names the gateway container every org network joins;
	// empty finds the compose project's "bifrost" service.
	sharedGatewayEnv = "HEIMDALL_SHARED_GATEWAY_CONTAINER"
	// sharedPlatformImagesEnv lists images every org may pull (comma
	// separated; a trailing "/" or ":" is a prefix).
	sharedPlatformImagesEnv = "HEIMDALL_SHARED_PLATFORM_IMAGES"
	// sharedBuilderName is the buildx builder the runner creates on
	// HEIMDALL_SHARED_BUILDKIT.
	sharedBuilderName = "heimdall-org-builder"
	// sharedNetworkPrefix starts every org's eval network name.
	sharedNetworkPrefix = "heimdall-evalnet-"
	// runtimeAuto picks gVisor when the host has it.
	runtimeAuto = "auto"
)

// hasSharedFlag reports whether args ask for shared runner mode.
func hasSharedFlag(args []string) bool {
	for _, a := range args {
		if a == "-shared" || a == "--shared" || a == "-shared=true" || a == "--shared=true" {
			return true
		}
	}
	return false
}

type sharedFlags struct {
	server, secretFile, runtime, name, config string
	builder, buildkit, gateway, projectsDir   string
	slots                                     int
	poll                                      time.Duration
	debug                                     bool
}

func parseSharedFlags(args []string) (*sharedFlags, error) {
	f := &sharedFlags{}
	fs := flag.NewFlagSet("runner start -shared", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool("shared", true, "shared runner mode")
	fs.StringVar(&f.server, "server", "", "hosted server URL")
	fs.StringVar(&f.secretFile, "secret-file", "", "file holding "+sharedpool.SecretEnv+"; mode 0600")
	fs.StringVar(&f.runtime, "runtime", runtimeAuto, "auto (gVisor when the host has runsc), runc or runsc")
	fs.IntVar(&f.slots, "slots", 1, "slots per org (capped by the org's max concurrent runs)")
	fs.DurationVar(&f.poll, "poll", 30*time.Second, "how often to fetch the assignments")
	fs.StringVar(&f.name, "name", "", "runner name prefix (default: shared-<host>)")
	fs.StringVar(&f.config, "config", defaultConfig, "bench config")
	fs.StringVar(&f.builder, "builder", os.Getenv(sharedBuilderEnv), "buildx builder for images from org Dockerfiles")
	fs.StringVar(&f.buildkit, "buildkit", os.Getenv(sharedBuildkitEnv), "isolated BuildKit address for images from org Dockerfiles")
	fs.StringVar(&f.gateway, "gateway-container", os.Getenv(sharedGatewayEnv), "gateway container every org network joins")
	fs.StringVar(&f.projectsDir, "projects-dir", os.Getenv(sharedMirrorEnv), "folder (same path on the Docker host) for downloaded org projects")
	fs.BoolVar(&f.debug, "debug", false, "debug logging")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case fs.NArg() != 0:
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case f.server == "" || f.secretFile == "":
		return nil, errors.New("-server and -secret-file are required")
	case f.runtime != runtimeAuto && f.runtime != quota.RuntimeRunc && f.runtime != quota.RuntimeRunsc:
		return nil, errors.New("-runtime must be auto, runc or runsc")
	case f.slots < 1 || f.slots > 64:
		return nil, errors.New("-slots must be 1 to 64")
	case f.poll < time.Second:
		return nil, errors.New("-poll must be at least 1s")
	}
	return f, nil
}

// gvisorMissing is the warning a shared runner logs when the host has no
// gVisor.
const gvisorMissing = "gVisor (runsc) is not installed on this Docker host: org agents run under runc and share the host kernel. " +
	"Install gVisor (https://gvisor.dev/docs/user_guide/install/), register it with Docker as runtime \"runsc\" and restart the shared runner. " +
	"Orgs whose limits require gVisor are not served until then."

// resolveSharedRuntime picks the container runtime and the assignment
// filter. auto: runsc when the host has it, serving every org; else runc,
// serving only runc orgs, with a warning. An explicit runsc needs runsc.
func resolveSharedRuntime(want string, hasRunsc bool) (runtime, filter, warning string, err error) {
	switch want {
	case quota.RuntimeRunsc:
		if !hasRunsc {
			return "", "", "", errors.New("-runtime runsc: the Docker host has no runsc runtime; install gVisor or use -runtime auto")
		}
		return quota.RuntimeRunsc, quota.RuntimeRunsc, "", nil
	case quota.RuntimeRunc:
		w := ""
		if hasRunsc {
			w = "gVisor (runsc) is installed but -runtime runc was given: org agents run under runc. Use -runtime auto to run them under gVisor."
		}
		return quota.RuntimeRunc, quota.RuntimeRunc, w, nil
	default:
		if hasRunsc {
			return quota.RuntimeRunsc, "", "", nil
		}
		return quota.RuntimeRunc, quota.RuntimeRunc, gvisorMissing, nil
	}
}

// dockerHasRunsc reports whether the Docker daemon lists a runsc runtime.
func dockerHasRunsc(ctx context.Context, d utils.Docker) bool {
	res, err := d.Run(ctx, "info", "--format", "{{json .Runtimes}}")
	if err != nil || res.ExitCode != 0 {
		return false
	}
	return runtimesHaveRunsc(res.Stdout)
}

func runtimesHaveRunsc(raw []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	_, ok := m[quota.RuntimeRunsc]
	return ok
}

// sharedCall makes one shared-secret call and decodes the JSON answer.
func sharedCall(ctx context.Context, c *http.Client, method, u, secret string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.Unmarshal(body, out)
}

// fetchAssignments asks the server for the shared runner's assignments.
func fetchAssignments(ctx context.Context, c *http.Client, server, secret, runtime string) (*sharedpool.Assignments, error) {
	u := strings.TrimRight(server, "/") + "/api/bench/admin/pool/assignments?runtime=" + url.QueryEscape(runtime)
	var out sharedpool.Assignments
	if err := sharedCall(ctx, c, http.MethodGet, u, secret, &out); err != nil {
		return nil, fmt.Errorf("assignments: %w", err)
	}
	return &out, nil
}

// claimSharedToken claims one org's fresh runner token.
func claimSharedToken(ctx context.Context, c *http.Client, server, secret, org, runtime string) (*sharedpool.ClaimedToken, error) {
	u := strings.TrimRight(server, "/") + "/api/bench/admin/pool/assignments/" + url.PathEscape(org) + "/token?runtime=" + url.QueryEscape(runtime)
	var out sharedpool.ClaimedToken
	if err := sharedCall(ctx, c, http.MethodPost, u, secret, &out); err != nil {
		return nil, fmt.Errorf("claim runner token for %s: %w", org, err)
	}
	if out.Token == "" || out.OrgID != org {
		return nil, fmt.Errorf("claim runner token for %s: empty answer", org)
	}
	return &out, nil
}

// sharedLoop is one org's worker loop.
type sharedLoop struct {
	slots  int
	policy *utils.DockerPolicy // nil in tests
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	tokenID string // the claimed token's id; "" until claimed
}

func (l *sharedLoop) claimed() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tokenID
}

func (l *sharedLoop) setClaimed(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tokenID = id
}

// sharedSlots is the slot count for one assignment.
func sharedSlots(max, flagSlots int) int {
	if max > 0 && max < flagSlots {
		return max
	}
	return flagSlots
}

// reconcileShared starts, restarts and stops loops so that exactly the
// assigned orgs run. A loop restarts when its slots change or when the
// org's live token is no longer the one it claimed (revoked or claimed by
// someone else) or when it ended on its own; its approved images update in
// place. start begins one org's loop.
func reconcileShared(loops map[string]*sharedLoop, as *sharedpool.Assignments, flagSlots int,
	start func(a sharedpool.Assignment, slots int) *sharedLoop) {
	want := map[string]sharedpool.Assignment{}
	if !as.KillSwitch {
		for _, a := range as.Assignments {
			want[a.OrgID] = a
		}
	}
	for org, l := range loops {
		a, ok := want[org]
		ended := false
		select {
		case <-l.done: // stopped on its own (claim or start failed): start again
			ended = true
		default:
		}
		if ok && !ended && sharedSlots(a.MaxConcurrentRuns, flagSlots) == l.slots {
			if id := l.claimed(); id == "" || id == a.TokenID {
				if l.policy != nil {
					l.policy.SetApproved(a.Images)
				}
				continue
			}
		}
		l.cancel()
		<-l.done
		delete(loops, org)
	}
	for org, a := range want {
		if _, ok := loops[org]; !ok {
			loops[org] = start(a, sharedSlots(a.MaxConcurrentRuns, flagSlots))
		}
	}
}

// sharedHost is what every org loop of one shared runner shares.
type sharedHost struct {
	base        *app
	f           *sharedFlags
	secret      string
	runtime     string // container runtime of every org
	filter      string // assignment filter ("" every org)
	builder     string // buildx builder for org images ("" refuses them)
	gateway     string // gateway container every org network joins
	gatewayHost string // the gateway's name as agents dial it
	trusted     []string
	platform    []string
	client      *http.Client
	host        string
	prefix      string
}

// sharedSplitList splits a comma separated list, dropping blanks.
func sharedSplitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// orgPolicy is the Docker policy of one org on this runner.
func (h *sharedHost) orgPolicy(org string, approved []string, projectRoot string) *utils.DockerPolicy {
	return &utils.DockerPolicy{
		OrgID: org, Runtime: h.runtime, Builder: h.builder, TagPrefix: utils.OrgImagePrefix(org),
		Trusted: h.trusted, Untrusted: []string{projectRoot}, Approved: append([]string(nil), approved...), Platform: h.platform,
	}
}

// orgNetworkName is the eval network of one org.
func orgNetworkName(org string) string { return sharedNetworkPrefix + org }

// ensureOrgNetwork creates the org's internal eval network when missing and
// connects the gateway to it under the name agents dial.
func ensureOrgNetwork(ctx context.Context, d utils.Docker, name, org, gateway, alias string) error {
	res, err := d.Run(ctx, "network", "inspect", "--format", "{{.Internal}}", name)
	if err != nil {
		return err
	}
	switch {
	case res.ExitCode != 0:
		if _, err := d.MustRun(ctx, "network", "create", "--internal", "--label", "heimdall=1", "--label", "heimdall.org="+org, name); err != nil {
			return fmt.Errorf("create network %s: %w", name, err)
		}
	case strings.TrimSpace(string(res.Stdout)) != "true":
		return fmt.Errorf("network %s exists but is not internal; remove it", name)
	}
	if gateway == "" {
		return nil
	}
	args := []string{"network", "connect"}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	res, err = d.Run(ctx, append(args, name, gateway)...)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "already exists") {
		return fmt.Errorf("connect gateway %s to %s: %s", gateway, name, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// removeOrgNetwork disconnects the gateway and removes the org's network
// (best effort: a network still in use stays).
func removeOrgNetwork(ctx context.Context, d utils.Docker, name, gateway string) {
	if gateway != "" {
		_, _ = d.Run(ctx, "network", "disconnect", "-f", name, gateway)
	}
	_, _ = d.Run(ctx, "network", "rm", name)
}

// findGateway names the compose project's gateway container: the "bifrost"
// service of the project this runner's own container belongs to.
func findGateway(ctx context.Context, d utils.Docker) string {
	host, _ := os.Hostname()
	if host == "" {
		return ""
	}
	res, err := d.Run(ctx, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.project"}}`, host)
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	project := strings.TrimSpace(string(res.Stdout))
	if project == "" {
		return ""
	}
	res, err = d.Run(ctx, "ps", "-q", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service=bifrost")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	ids := strings.Fields(string(res.Stdout))
	if len(ids) != 1 {
		return ""
	}
	return ids[0]
}

// ensureBuilder returns the buildx builder for org images: -builder as
// given, else a remote builder on -buildkit (created when missing), else ""
// (org Dockerfiles are refused).
func ensureBuilder(ctx context.Context, d utils.Docker, builder, buildkit string) (string, error) {
	if builder != "" || buildkit == "" {
		return builder, nil
	}
	if res, err := d.Run(ctx, "buildx", "inspect", sharedBuilderName); err == nil && res.ExitCode == 0 {
		return sharedBuilderName, nil
	}
	if _, err := d.MustRun(ctx, "buildx", "create", "--name", sharedBuilderName, "--driver", "remote", buildkit); err != nil {
		return "", fmt.Errorf("create the isolated image builder on %s: %w", buildkit, err)
	}
	return sharedBuilderName, nil
}

// gatewayAlias is the host name agents dial the gateway by (the agent URL's
// host), so it resolves on every org network.
func gatewayAlias(agentURL string) string {
	u, err := url.Parse(agentURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func runSharedRunner(args []string) int {
	f, err := parseSharedFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "runner start -shared: %v\n%s\n", err, sharedRunnerUsage)
		return exitUsage
	}
	secret, err := readTokenFile(f.secretFile)
	if err != nil {
		return fail("%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The base app plans nothing for an org; it holds the host-level docker
	// (networks, builder) and the config every org app starts from.
	a, err := newApp(ctx, f.config, appOptions{debug: f.debug})
	if err != nil {
		return fail("%v", err)
	}
	h := &sharedHost{base: a, f: f, secret: secret, client: &http.Client{Timeout: 2 * time.Minute},
		platform: sharedSplitList(os.Getenv(sharedPlatformImagesEnv)), gatewayHost: gatewayAlias(a.cfg.Gateway.AgentURL)}
	var warning string
	h.runtime, h.filter, warning, err = resolveSharedRuntime(f.runtime, dockerHasRunsc(ctx, a.docker))
	if err != nil {
		return fail("%v", err)
	}
	if warning != "" {
		a.logger.Warn("shared runner: " + warning)
	}
	if h.builder, err = ensureBuilder(ctx, a.docker, f.builder, f.buildkit); err != nil {
		return fail("%v", err)
	}
	if h.builder == "" {
		a.logger.Warn("shared runner: no isolated image builder (" + sharedBuildkitEnv + "): images from org Dockerfiles are refused; built-in and approved images still run")
	}
	h.gateway = f.gateway
	if h.gateway == "" {
		h.gateway = findGateway(ctx, a.docker)
	}
	if h.gateway == "" {
		a.logger.Warn("shared runner: gateway container not found (" + sharedGatewayEnv + "): org networks cannot reach the model gateway")
	}
	h.trusted = []string{a.cfg.Root}
	if lib := suite.LibraryDir(); lib != "" {
		h.trusted = append(h.trusted, lib)
	}
	if f.projectsDir == "" {
		f.projectsDir = filepath.Join(a.cfg.Path(a.cfg.RunsRoot), "..", "projects")
	}
	host, _ := os.Hostname()
	h.host, h.prefix = host, f.name
	if h.prefix == "" {
		h.prefix = "shared-" + host
	}
	var wg sync.WaitGroup
	start := func(as sharedpool.Assignment, slots int) *sharedLoop {
		lctx, cancel := context.WithCancel(ctx)
		l := &sharedLoop{slots: slots, cancel: cancel, done: make(chan struct{})}
		root := filepath.Join(f.projectsDir, as.OrgID)
		l.policy = h.orgPolicy(as.OrgID, as.Images, root)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(l.done)
			h.serveOrg(lctx, as.OrgID, slots, root, l)
		}()
		return l
	}
	loops := map[string]*sharedLoop{}
	a.logger.Info(fmt.Sprintf("shared runner %s: runtime %s, %d slot(s) per org, server %s", h.prefix, h.runtime, f.slots, f.server))
	tick := time.NewTicker(f.poll)
	defer tick.Stop()
	for {
		as, err := fetchAssignments(ctx, h.client, f.server, secret, h.filter)
		switch {
		case err != nil && ctx.Err() == nil:
			a.logger.Warn("shared runner: assignments not fetched; keeping the current ones", "error", err.Error())
		case err == nil:
			if as.KillSwitch && len(loops) > 0 {
				a.logger.Warn("shared runner: kill switch is on; stopping every org's worker")
			}
			reconcileShared(loops, as, f.slots, start)
		}
		select {
		case <-ctx.Done():
			for _, l := range loops {
				l.cancel()
			}
			wg.Wait()
			return exitOK
		case <-tick.C:
		}
	}
}

// serveOrg is one org's worker loop: claim the org's token, prepare its
// network and project, then serve leases until ctx ends.
func (h *sharedHost) serveOrg(ctx context.Context, org string, slots int, root string, l *sharedLoop) {
	a := h.base
	if tenancy.ValidOrgID(org) != nil {
		a.logger.Error("shared runner: invalid org id in the assignments", "org", org)
		return
	}
	claim, err := claimSharedToken(ctx, h.client, h.f.server, h.secret, org, h.filter)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("shared runner: org not served", "org", org, "error", err.Error())
		}
		return
	}
	l.setClaimed(claim.TokenID)
	network := orgNetworkName(org)
	if err := ensureOrgNetwork(ctx, a.docker, network, org, h.gateway, h.gatewayHost); err != nil {
		a.logger.Error("shared runner: org network", "org", org, "error", err.Error())
		return
	}
	defer removeOrgNetwork(context.Background(), a.docker, network, h.gateway)
	proj := &orgProject{server: h.f.server, token: claim.Token, dst: root, client: h.client}
	if err := proj.refresh(ctx); err != nil {
		a.logger.Warn("shared runner: org project not downloaded; built-in suites only until it is", "org", org, "error", err.Error())
	}
	docker := utils.Docker{Binary: a.docker.Binary, Policy: l.policy}
	oa, err := newApp(ctx, h.f.config, appOptions{gateway: true, debug: h.f.debug, docker: &docker, evalNetwork: network})
	if err != nil {
		a.logger.Error("shared runner: org worker not started", "org", org, "error", err.Error())
		return
	}
	name := h.prefix + "-" + org
	if len(name) > 128 {
		name = name[:128]
	}
	reg, planRoot := sharedOrgProject(oa, root)
	plan := engine.NewPlanner(reg, planRoot, oa.logger, engine.WithIgnoreQuarantine(), engine.WithoutGPUCheck(), engine.WithAgentLibrary(suite.LibraryDir())).Plan
	w, err := engine.NewWorker(reg, engine.NewPipeline(reg, planRoot, oa.logger),
		sharedEnsureImages(oa, reg, planRoot, proj.refresh, plan),
		engine.WorkerOptions{Server: h.f.server, Token: claim.Token, WorkerID: name, Host: h.host, Version: benchVersion,
			Slots: slots, Logger: oa.logger, Pool: sharedpool.Pool, Labels: []string{"shared", "runtime=" + h.runtime}})
	// No Jobs: hosted CLI jobs would run the docker CLI outside the org's
	// policy; the worker refuses them and they go to the org's own workers.
	if err != nil {
		a.logger.Error("shared runner: worker not started", "org", org, "error", err.Error())
		return
	}
	a.logger.Info("shared runner: serving org", "org", org, "slots", slots, "runtime", h.runtime, "network", network)
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		a.logger.Error("shared runner: worker stopped", "org", org, "error", err.Error())
	}
}

// orgProject keeps one org's project folder in step with the server's
// copy, downloaded with the org's runner token.
type orgProject struct {
	server, token, dst string
	client             *http.Client

	mu   sync.Mutex
	etag string
}

// refresh downloads the project when it changed (ETag) and syncs it into
// dst, which keeps its path so running containers' bind mounts stay valid.
func (p *orgProject) refresh(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.server, "/")+sharedpool.ProjectPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if p.etag != "" {
		if _, err := os.Stat(p.dst); err == nil {
			req.Header.Set("If-None-Match", p.etag)
		}
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil
	case http.StatusOK:
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("project: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := os.MkdirAll(filepath.Dir(p.dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(p.dst), "."+filepath.Base(p.dst)+".incoming-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := sharedpool.ExtractProject(resp.Body, tmp); err != nil {
		return err
	}
	if err := mirrorTree(tmp, p.dst); err != nil {
		return err
	}
	p.etag = resp.Header.Get("ETag")
	return nil
}

// sharedOrgProject is the registry and project root the shared runner plans
// an org's runs with: the org's downloaded project at root when it holds
// suites, else the runner's own checkout. Either way the built-in library
// is layered underneath.
func sharedOrgProject(a *app, root string) (*schemas.Registry, string) {
	if root == "" {
		return withLibrary(a.reg, a.cfg.Path(a.cfg.BenchmarksRoot)), a.cfg.Root
	}
	if fi, err := os.Stat(filepath.Join(root, "benchmarks")); err != nil || !fi.IsDir() {
		return withLibrary(a.reg, a.cfg.Path(a.cfg.BenchmarksRoot)), a.cfg.Root
	}
	reg := *a.reg
	// The built-in library is a host path mounted at the same path here, so
	// it needs no copy (suite/library.go).
	reg.Loaders = suite.Layered(filepath.Join(root, "benchmarks"), suite.LibraryBenchmarks(suite.LibraryDir()))
	reg.Adapters = adapters.Builtin(adapters.CommandConfig{
		GatewayAgentURL: a.cfg.Gateway.AgentURL, EvalNetwork: a.cfg.Networks.Eval, BenchRoot: root,
		GatewayPublicURL: a.cfg.Gateway.PublicURL,
	})
	return &reg, root
}

// withLibrary is reg with its suite loaders over benchRoot layered on the
// server's built-in benchmark library (suite/library.go); reg itself when
// HEIMDALL_LIBRARY_DIR is unset.
func withLibrary(reg *schemas.Registry, benchRoot string) *schemas.Registry {
	lib := suite.LibraryDir()
	if lib == "" {
		return reg
	}
	out := *reg
	out.Loaders = suite.Layered(benchRoot, suite.LibraryBenchmarks(lib))
	return &out
}

// mirrorTree copies src into dst: new and changed files (size or mtime),
// folders, modes. Links are skipped, .git and .heimdall left out, and files
// gone from src removed from dst.
func mirrorTree(src, dst string) error {
	seen := map[string]bool{}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		skip, dir := mirrorSkip(rel, d.IsDir())
		if !skip && rel == ".heimdall" { // only enter it for agent version snapshots
			if fi, err := os.Stat(filepath.Join(path, "agent-versions")); err != nil || !fi.IsDir() {
				skip, dir = true, true
			}
		}
		if skip {
			if dir {
				return filepath.SkipDir
			}
			return nil
		}
		seen[rel] = true
		out := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return nil
		case d.IsDir():
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			return os.Chmod(out, 0o755) // the runner's umask must not narrow it

		case !d.Type().IsRegular():
			return nil
		}
		si, err := d.Info()
		if err != nil {
			return err
		}
		if di, err := os.Stat(out); err == nil && di.Size() == si.Size() && di.ModTime().Equal(si.ModTime()) {
			if di.Mode().Perm()&0o444 != 0o444 { // a mirror written before the mode fix
				return os.Chmod(out, di.Mode().Perm()|0o444)
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		tmp := out + ".mirror-tmp"
		// Readable by everyone (task containers run as other users), and
		// executable when the source is: explicit, so the umask cannot
		// narrow it to owner-only (which made /srv/server.py unreadable).
		mode := si.Mode().Perm() | 0o444
		if si.Mode().Perm()&0o111 != 0 {
			mode |= 0o111
		}
		if err := os.WriteFile(tmp, b, mode); err != nil {
			return err
		}
		if err := os.Chmod(tmp, mode); err != nil {
			return err
		}
		if err := os.Chtimes(tmp, si.ModTime(), si.ModTime()); err != nil {
			return err
		}
		return os.Rename(tmp, out)
	})
	if err != nil {
		return err
	}
	var stale []string
	_ = filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dst, path)
		if rel != "." && !seen[rel] {
			stale = append(stale, path)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	for _, p := range stale {
		_ = os.RemoveAll(p)
	}
	return nil
}

// mirrorSkip reports whether mirrorTree leaves rel out (dir: skip the whole
// folder): the project transfer's rule (sharedpool.ProjectSkip), so .git,
// .heimdall (except frozen agent versions, which a pinned arm reads when
// the runner re-plans) and the release log never reach the runner.
func mirrorSkip(rel string, isDir bool) (skip, dir bool) {
	return sharedpool.ProjectSkip(rel, isDir)
}

// sharedEnsureImages refreshes the org's project, then builds the suite's
// missing images (base, services) before planning a leased experiment, as
// `suite admit` does locally: an org that added a suite on the website has
// no other way to build them, and a missing image would otherwise fail
// every run at prepare as a pull error. The org's Docker policy decides
// where each build runs (utils/dockerpolicy.go): org Dockerfiles only on
// the isolated builder, under the org's image namespace.
func sharedEnsureImages(a *app, reg *schemas.Registry, root string, refresh func(context.Context) error, plan engine.Replanner) engine.Replanner {
	var mu sync.Mutex
	built := map[string]bool{}
	return func(ctx context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
		if refresh != nil {
			if err := refresh(ctx); err != nil {
				a.logger.Warn("shared runner: org project not refreshed; planning with the last copy", "error", err.Error())
			}
		}
		if e != nil && e.Suite != "" {
			mu.Lock()
			done := built[e.Suite]
			mu.Unlock()
			if !done {
				if s, err := engine.LoadSuite(ctx, reg, e.Suite); err == nil {
					cfg := *a.cfg
					cfg.Root = root
					if err := ops.EnsureImages(ctx, &cfg, a.docker, s, io.Discard); err != nil {
						a.logger.Warn("shared runner: build suite images", "suite", e.Suite, "error", err.Error())
					} else {
						mu.Lock()
						built[e.Suite] = true
						mu.Unlock()
					}
				}
			}
		}
		return plan(ctx, e)
	}
}
