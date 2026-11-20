package environments

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// Tool proxy and service settings. The proxy's name, host name, port, command
// and file paths are fixed between the environment kernel, the tool proxy and
// the tool probe layer (docs/scenarios-design.md).
const (
	ToolProxyHost         = "toolproxy"
	ToolProxyPort         = 9200
	DefaultToolProxyImage = "bench/bifrost-bench:dev"
	ToolsFile             = "tools.jsonl" // the proxy's ledger, copied into the run folder by ExportState
	StateFolder           = "state"       // state/<service>/<export> in the run folder
	toolProxyLedger       = "/ledger/tools.jsonl"
	toolProxyCfg          = "/cfg"
	defaultReadyS         = 60
	toolProxyReadyS       = 30
	defaultStateCap       = 16 << 20
	maxLedgerBytes        = 64 << 20
	readyPoll             = 500 * time.Millisecond
	readyCallTimeout      = 15 * time.Second
	exportTimeout         = 2 * time.Minute
)

var (
	serviceIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
	exportIDRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// readyScript runs in the probe container: "tcp host port" connects, "http
// url" must answer 2xx.
const readyScript = `import socket, sys, urllib.request
a = sys.argv[1:]
try:
    if a[0] == "tcp":
        socket.create_connection((a[1], int(a[2])), timeout=3).close()
    else:
        r = urllib.request.urlopen(a[1], timeout=5)
        if not 200 <= r.status < 300:
            print("status %d" % r.status, file=sys.stderr)
            sys.exit(1)
except Exception as e:
    print(e, file=sys.stderr)
    sys.exit(1)
`

// ServiceError is a service, or the tool proxy, that could not be built,
// started or made ready. The run kernel reports such a run aborted_infra:
// the agent never started.
type ServiceError struct {
	Service string
	Err     error
}

// Error implements error.
func (e *ServiceError) Error() string { return "service " + e.Service + ": " + e.Err.Error() }

// Unwrap returns the cause.
func (e *ServiceError) Unwrap() error { return e.Err }

// runService is one service of a run with the folder its Files and Build are
// relative to.
type runService struct {
	spec    schemas.ServiceSpec
	base    string // suite root for suite services, task folder for task services
	network bool   // its image build may use the network
}

// toolRoute is one entry of the tool proxy's routes.json.
type toolRoute struct {
	Service  string         `json:"service"`
	Upstream string         `json:"upstream"`
	Protocol string         `json:"protocol"`
	MCPPath  string         `json:"mcp_path,omitempty"`
	External *externalRoute `json:"external,omitempty"` // External APIs area: set for protocol external (external.go)
}

// StartServices starts the run's services, suite services first and then
// task services, in file order, each ready before the next starts. They run
// on a per-run internal network (no route out) that the agent joins in Exec;
// a hidden service sits only on a second internal network that every other
// service also joins, so the agent cannot reach it. A proxied service sits
// only on that second network too, which the tool proxy also joins, so the
// agent reaches it through the proxy and its ledger, never directly. When
// any service is proxied the tool proxy starts last and the handles of
// proxied services carry its URL. A run without services gets no network and nil results. On
// failure the error is a *ServiceError naming the service; the results
// collected so far are returned with it, and Destroy removes what started.
func (e *dockerWorkspace) StartServices(ctx context.Context, rc *schemas.RunContext) ([]schemas.ServiceHandle, []schemas.ServiceResult, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, nil, err
	}
	svcs, err := runServices(rc)
	if err != nil {
		return nil, nil, err
	}
	svcs = e.withMailbox(rc, svcs) // Multi-agent area: a team run's mailbox (mailbox.go)
	// External APIs area: the run's external APIs (external.go); nil for a run without them.
	ext, err := externalAPIs(rc, svcs)
	if err != nil {
		return nil, nil, &ServiceError{Service: "external", Err: err}
	}
	if len(svcs) == 0 && len(ext) == 0 {
		return nil, nil, nil
	}
	hidden := false
	for _, s := range svcs {
		hidden = hidden || s.spec.Hidden || proxied(s.spec)
	}
	if err := e.createNetworks(ctx, expID, runID, hidden); err != nil {
		return nil, nil, &ServiceError{Service: "network", Err: err}
	}
	if err := e.createComposeVolumes(ctx, expID, runID, svcs); err != nil { // X2: the run's compose named volumes (compose.go)
		return nil, nil, &ServiceError{Service: "volumes", Err: err}
	}
	probe, err := e.startProbe(ctx, rc, expID, runID, hidden)
	if err != nil {
		return nil, nil, &ServiceError{Service: "probe", Err: err}
	}
	defer e.removeContainers(ctx, probe)
	var handles []schemas.ServiceHandle
	var results []schemas.ServiceResult
	for _, s := range svcs {
		h, r, err := e.startService(ctx, rc, expID, runID, s, hidden, probe)
		results = append(results, r)
		if err != nil {
			return handles, results, &ServiceError{Service: s.spec.ID, Err: err}
		}
		handles = append(handles, h)
	}
	routes, err := proxyRoutes(svcs)
	if err != nil {
		return handles, results, err
	}
	routes = append(routes, externalRoutes(runID, ext)...) // External APIs area
	if len(routes) == 0 {
		e.startEventCollector(ctx, rc, expID, runID, probe) // Agent events area: the collector alone (eventcollector.go)
		return handles, results, nil
	}
	if err := e.startExternalEgress(ctx, rc, expID, runID, ext); err != nil { // External APIs area: forwarders for live and record APIs
		return handles, results, &ServiceError{Service: "external", Err: err}
	}
	if err := e.startToolProxy(ctx, rc, expID, runID, routes, probe, ext, hidden); err != nil {
		return handles, results, &ServiceError{Service: ToolProxyHost, Err: err}
	}
	e.markCollector(runID) // Agent events area: the tool proxy serves the collector (eventcollector.go)
	for i := range handles {
		if proxied(handles[i].Spec) {
			handles[i].URL = toolProxyURL(handles[i].Spec.ID)
			results[i].Proxied = true
		}
	}
	xh, xr := externalHandles(runID, e.toolProxyImage(), ext) // External APIs area
	handles, results = append(handles, xh...), append(results, xr...)
	if err := e.startRemoteTools(ctx, rc, expID, runID, handles); err != nil { // Remote coverage: publish the tool proxy to a remote agent (remotetools.go)
		return handles, results, &ServiceError{Service: "remote_tools", Err: err}
	}
	e.cfg.Logger.Debug("services ready", "run", runID, "services", len(handles), "proxied", len(routes))
	return handles, results, nil
}

// ExportState reads every export of every service into
// state/<service>/<export> in the run folder, and copies the tool proxy's
// ledger to tools.jsonl when the run has one. A single export that fails or
// is over its cap is recorded in its StateFile.Error, not returned: the run
// is graded with that state reported as missing. Only a run folder that
// cannot be written is an error.
func (e *dockerWorkspace) ExportState(ctx context.Context, rc *schemas.RunContext) ([]schemas.StateFile, error) {
	_, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Dir == "" {
		return nil, fmt.Errorf("export state for run %s: no run folder", runID)
	}
	svcs, err := runServices(rc)
	if err != nil {
		return nil, fmt.Errorf("export state for run %s: %w", runID, err)
	}
	svcs = e.withMailbox(rc, svcs) // Multi-agent area: a team run's mailbox (mailbox.go)
	var files []schemas.StateFile
	anyProxied := false
	for _, s := range svcs {
		anyProxied = anyProxied || proxied(s.spec)
		for _, x := range s.spec.Export {
			f, err := e.exportOne(ctx, runID, rc.Dir, s.spec, x)
			if err != nil {
				return files, fmt.Errorf("export state for run %s: %w", runID, err)
			}
			files = append(files, f)
		}
	}
	anyProxied = anyProxied || len(utils.ExternalAPIs(rc.Suite, rc.Task)) > 0 // External APIs area
	if anyProxied {
		if err := e.copyLedger(ctx, runID, rc.Dir); err != nil {
			e.cfg.Logger.Warn("tool proxy ledger not copied; tool evidence is unknown", "run", runID, "error", err.Error())
		}
	}
	e.captureServiceLogs(ctx, runID, rc.Dir, svcs)           // X2: compose services' output as services/<id>.log (compose.go)
	e.copyAgentEvents(ctx, runID, rc.Dir)                    // Agent events area: the collector's ledger (eventcollector.go)
	if err := e.exportExternal(ctx, rc, runID); err != nil { // External APIs area: record mode cassettes
		return files, fmt.Errorf("export state for run %s: %w", runID, err)
	}
	gw, err := e.finishServiceKeys(ctx, rc, runID) // W3 customer support: services' own gateway rows, keys revoked (servicegateway.go)
	files = append(files, gw...)
	if err != nil {
		return files, fmt.Errorf("export state for run %s: %w", runID, err)
	}
	return files, nil
}

// runServices lists the run's services, suite first, with their base folders,
// and refuses ids that are invalid, reserved or repeated.
func runServices(rc *schemas.RunContext) ([]runService, error) {
	var out []runService
	seen := map[string]bool{}
	add := func(specs []schemas.ServiceSpec, base string, network bool) error {
		for _, s := range specs {
			if err := checkService(s); err != nil {
				return err
			}
			if seen[s.ID] {
				return fmt.Errorf("service id %q is used twice", s.ID)
			}
			seen[s.ID] = true
			out = append(out, runService{spec: s, base: base, network: network})
		}
		return nil
	}
	if rc.Suite != nil {
		if err := add(rc.Suite.Services, rc.Suite.Root, false); err != nil {
			return nil, err
		}
	}
	if rc.Task != nil {
		network := rc.Task.Environment != nil && rc.Task.Environment.BuildNetwork
		if err := add(rc.Task.Services, rc.Task.Dir, network); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// checkService validates one spec. It is pure.
func checkService(s schemas.ServiceSpec) error {
	switch {
	case !serviceIDRe.MatchString(s.ID) || s.ID == ToolProxyHost:
		return fmt.Errorf("service id %q must match %s and not be %s", s.ID, serviceIDRe, ToolProxyHost)
	case s.Image == "" && s.Build == nil:
		return fmt.Errorf("service %s has neither image nor build", s.ID)
	case s.Build != nil && s.Build.Dockerfile == "":
		return fmt.Errorf("service %s build has no dockerfile", s.ID)
	case s.Port < 0 || s.Port > 65535:
		return fmt.Errorf("service %s port %d is out of range", s.ID, s.Port)
	case s.Ready != nil && len(s.Ready.Argv) > 0 && s.Ready.HTTP != "":
		return fmt.Errorf("service %s ready check sets both argv and http", s.ID)
	case s.Ready != nil && s.Ready.HTTP != "" && (s.Port == 0 || !strings.HasPrefix(s.Ready.HTTP, "/")):
		return fmt.Errorf("service %s http ready check needs a port and a path starting with /", s.ID)
	case proxied(s) && s.Port == 0:
		return fmt.Errorf("service %s is proxied but has no port", s.ID)
	}
	for p := range s.Files {
		if !path.IsAbs(p) || path.Clean(p) != p || p == "/" {
			return fmt.Errorf("service %s file %q must be an absolute clean container path", s.ID, p)
		}
	}
	seen := map[string]bool{}
	for _, x := range s.Export {
		switch {
		case !exportIDRe.MatchString(x.ID) || seen[x.ID]:
			return fmt.Errorf("service %s export id %q is invalid or repeated", s.ID, x.ID)
		case (len(x.Argv) > 0) == (x.Path != ""):
			return fmt.Errorf("service %s export %s needs exactly one of argv and path", s.ID, x.ID)
		case x.Path != "" && !path.IsAbs(x.Path):
			return fmt.Errorf("service %s export %s path must be absolute", s.ID, x.ID)
		}
		seen[x.ID] = true
	}
	return nil
}

// proxied reports whether the agent reaches the service through the tool
// proxy. A hidden service is never proxied: the agent must not reach it.
func proxied(s schemas.ServiceSpec) bool {
	return !s.Hidden && utils.ServiceProxied(s)
}

// offAgentNetwork reports whether the service sits only on the hidden
// network, out of the agent's reach: a hidden service, and a proxied one,
// which the agent must reach through the tool proxy so every call is in the
// ledger.
func offAgentNetwork(s schemas.ServiceSpec) bool {
	return s.Hidden || proxied(s)
}

// proxyRoutes builds routes.json entries for the proxied services. It is
// pure.
func proxyRoutes(svcs []runService) ([]toolRoute, error) {
	var routes []toolRoute
	for _, s := range svcs {
		if !proxied(s.spec) {
			continue
		}
		r := toolRoute{Service: s.spec.ID, Upstream: upstreamURL(s.spec), Protocol: "http"}
		if s.spec.Role == schemas.ServiceRoleMCP {
			r.Protocol, r.MCPPath = "mcp", utils.DefaultMCPPath
			if s.spec.MCP != nil && s.spec.MCP.Path != "" {
				r.MCPPath = s.spec.MCP.Path
			}
			if s.spec.MCP != nil && s.spec.MCP.Transport != "" && s.spec.MCP.Transport != "http" {
				return nil, fmt.Errorf("service %s mcp transport %q is not http", s.spec.ID, s.spec.MCP.Transport)
			}
		}
		routes = append(routes, r)
	}
	return routes, nil
}

// createNetworks replaces any leftover run networks with fresh internal
// ones: the service network the agent joins and, when a service is hidden or proxied,
// the hidden network only services and the tool proxy join.
func (e *dockerWorkspace) createNetworks(ctx context.Context, expID, runID string, hidden bool) error {
	nets := []string{serviceNetwork(runID)}
	if hidden {
		nets = append(nets, hiddenNetwork(runID))
	}
	for _, n := range nets {
		if _, err := e.cfg.Docker.Run(ctx, "network", "rm", n); err != nil {
			return fmt.Errorf("remove stale network %s: %w", n, err)
		}
		args := append([]string{"network", "create", "--internal"}, utils.LabelArgs(expID, runID)...)
		if _, err := e.cfg.Docker.MustRun(ctx, append(args, n)...); err != nil {
			return fmt.Errorf("create network %s: %w", n, err)
		}
	}
	return nil
}

// startProbe starts the container ready checks run in: the helper image,
// idle, on the service networks, with nothing but python3 and sleep used.
func (e *dockerWorkspace) startProbe(ctx context.Context, rc *schemas.RunContext, expID, runID string, hidden bool) (string, error) {
	name := helperName(runID, "svcprobe")
	args := append([]string{"run", "-d", "--name", name}, utils.LabelArgs(expID, runID)...)
	args = append(args, "--network", serviceNetwork(runID), "--user", "65534:65534", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges", "--read-only", "--pids-limit", "64", "--memory", "128m",
		"--log-driver", "none", "--entrypoint", "sleep", e.helperImage(rc), "infinity")
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		e.removeContainers(ctx, name)
		return "", fmt.Errorf("start probe container: %w", err)
	}
	if hidden {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", hiddenNetwork(runID), name); err != nil {
			e.removeContainers(ctx, name)
			return "", fmt.Errorf("connect probe container: %w", err)
		}
	}
	return name, nil
}

// startService builds or finds the image, creates the container on its
// networks, copies its files in, starts it and waits until it is ready.
func (e *dockerWorkspace) startService(ctx context.Context, rc *schemas.RunContext, expID, runID string, s runService, hidden bool, probe string) (schemas.ServiceHandle, schemas.ServiceResult, error) {
	spec := s.spec
	res := schemas.ServiceResult{ID: spec.ID, Role: spec.Role, Image: spec.Image}
	h := schemas.ServiceHandle{Spec: spec, Container: serviceContainer(runID, spec.ID), Host: spec.ID}
	img, err := serviceImage(rc, s)
	if err != nil {
		res.Error = err.Error()
		return h, res, err
	}
	res.Image = img.Tag
	if res.ImageID, err = e.cfg.Docker.EnsureImage(ctx, img, io.Discard); err != nil {
		res.Error = err.Error()
		return h, res, err
	}
	if err := e.createService(ctx, rc, expID, runID, s, img.Tag, hidden); err != nil {
		res.Error = err.Error()
		return h, res, err
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "start", h.Container); err != nil {
		res.Error = err.Error()
		return h, res, fmt.Errorf("start: %w", err)
	}
	h.Network = serviceNetwork(runID) // X2: where the verifier finds it (compose.go)
	if offAgentNetwork(spec) {
		h.Network = hiddenNetwork(runID)
	}
	if runOnce(spec) { // X2: a one-shot compose service must exit 0 before the next one starts (compose.go)
		if err := e.waitDone(ctx, h.Container); err != nil {
			res.Error = err.Error()
			return h, res, err
		}
		res.Ready = true
		return h, res, nil
	}
	if err := e.waitReady(ctx, h.Container, spec, probe); err != nil {
		res.Error = err.Error()
		return h, res, err
	}
	res.Ready = true
	if !spec.Hidden && spec.Port > 0 {
		h.URL, h.Upstream = upstreamURL(spec), upstreamURL(spec)
	} else if spec.Port > 0 {
		h.Upstream = upstreamURL(spec)
	}
	return h, res, nil
}

// serviceImage is the service's prebuilt image, or its Dockerfile build
// tagged bench/svc-<suite>-<service>:<content hash>.
func serviceImage(rc *schemas.RunContext, s runService) (utils.LocalImage, error) {
	if s.spec.Build == nil {
		return utils.LocalImage{Tag: s.spec.Image}, nil
	}
	suiteID := ""
	if rc.Suite != nil {
		suiteID = rc.Suite.ID
	}
	img, err := utils.BuildImageSpec(s.base, s.spec.Build.Dockerfile, s.spec.Build.Context, "svc-"+suiteID+"-"+s.spec.ID, s.network)
	if err != nil {
		return utils.LocalImage{}, fmt.Errorf("image: %w", err)
	}
	return img, nil
}

// createService creates the service container (not started) and copies its
// Files in. Its environment is the spec's plus the probe's ServiceEnv for
// this service. Services keep the image's user and docker's default
// capabilities (database images switch users at start), without new
// privileges, with process and memory limits.
func (e *dockerWorkspace) createService(ctx context.Context, rc *schemas.RunContext, expID, runID string, s runService, image string, hidden bool) error {
	spec, name := s.spec, serviceContainer(runID, s.spec.ID)
	e.removeContainers(ctx, name)
	primary := serviceNetwork(runID)
	if offAgentNetwork(spec) {
		primary = hiddenNetwork(runID)
	}
	args := append([]string{"create", "--name", name}, utils.LabelArgs(expID, runID)...)
	pids, mem := serviceLimits(spec) // X2: a compose file may lower the caps (compose.go)
	args = append(args, "--network", primary, "--network-alias", spec.ID, "--hostname", spec.ID,
		"--security-opt", "no-new-privileges", "--pids-limit", pids, "--memory", mem,
		"--log-driver", "json-file", "--log-opt", "max-size=1m", "--log-opt", "max-file=1")
	args = append(args, composeCreateArgs(runID, spec)...) // X2: aliases, user, workdir, tmpfs, volumes, cpus, entrypoint (compose.go)
	env := serviceEnv(rc, spec)
	for _, k := range sortedKeys(env) {
		args = append(args, "-e", k+"="+env[k])
	}
	gwArgs, gwDone, err := e.prepareServiceGateway(ctx, rc, runID, spec) // W3 customer support: the service's own key (servicegateway.go)
	defer gwDone()
	if err != nil {
		return err
	}
	args = append(args, gwArgs...)
	args = append(args, image)
	args = append(args, serviceArgv(spec)...) // X2: the rest of a compose entrypoint, then the command (compose.go)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := e.connectGatewayNetwork(ctx, rc, spec, name); err != nil { // W3 customer support
		return err
	}
	if hidden && !offAgentNetwork(spec) {
		connect := append([]string{"network", "connect", "--alias", spec.ID}, hiddenAliasArgs(spec)...) // X2: compose aliases (compose.go)
		if _, err := e.cfg.Docker.MustRun(ctx, append(connect, hiddenNetwork(runID), name)...); err != nil {
			return fmt.Errorf("connect hidden network: %w", err)
		}
	}
	if len(spec.Files) == 0 {
		return nil
	}
	info, err := InspectImage(ctx, e.cfg.Docker, image)
	if err != nil {
		return err
	}
	uid, gid := numericUser(info.User)
	if err := e.copyFiles(ctx, name, s.base, spec.Files, uid, gid); err != nil {
		return fmt.Errorf("copy files: %w", err)
	}
	return nil
}

// serviceEnv is the spec's environment plus, when the run has a probe, the
// probe's variables for this service, which win. It is pure.
func serviceEnv(rc *schemas.RunContext, spec schemas.ServiceSpec) map[string]string {
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	if simuser.SimulatorBacked(spec) { // W3 customer support: the effective user simulator settings win over the spec's
		for k, v := range simuser.ServiceEnv(simuser.Effective(rc.Suite, rc.Experiment)) {
			env[k] = v
		}
	}
	if rc.Probe != nil {
		for k, v := range rc.Probe.Apply.ServiceEnv[spec.ID] {
			env[k] = v
		}
	}
	return env
}

// waitReady polls the service's ready check until it passes, the service
// exits, or the timeout (ready.timeout_s, default 60 s) ends.
func (e *dockerWorkspace) waitReady(ctx context.Context, name string, spec schemas.ServiceSpec, probe string) error {
	timeout := defaultReadyS * time.Second
	if spec.Ready != nil && spec.Ready.TimeoutS > 0 {
		timeout = time.Duration(spec.Ready.TimeoutS) * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		running, code, err := e.containerState(ctx, name)
		if err != nil {
			return err
		}
		if !running {
			return fmt.Errorf("exited with code %d before it was ready: %s", code, e.logTail(ctx, name))
		}
		detail := e.readyOnce(ctx, name, spec, probe)
		if detail == "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready after %s: %s", timeout, detail)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPoll):
		}
	}
}

// readyOnce runs the ready check once and returns "" when it passed, else why
// not. Without a ready check a service with a port is ready when the port
// accepts a connection, and one without a port when it is running.
func (e *dockerWorkspace) readyOnce(ctx context.Context, name string, spec schemas.ServiceSpec, probe string) string {
	cctx, cancel := context.WithTimeout(ctx, readyCallTimeout)
	defer cancel()
	var args []string
	switch {
	case spec.Ready != nil && len(spec.Ready.Argv) > 0:
		args = append([]string{"exec", name}, spec.Ready.Argv...)
	case spec.Ready != nil && spec.Ready.HTTP != "":
		args = []string{"exec", probe, "python3", "-c", readyScript, "http", upstreamURL(spec) + spec.Ready.HTTP}
	case spec.Port > 0:
		args = []string{"exec", probe, "python3", "-c", readyScript, "tcp", spec.ID, strconv.Itoa(spec.Port)}
	default:
		return ""
	}
	return e.check(cctx, args)
}

// check runs one docker call and returns "" on exit 0, else its error.
func (e *dockerWorkspace) check(ctx context.Context, args []string) string {
	res, err := e.cfg.Docker.Run(ctx, args...)
	switch {
	case err != nil:
		return err.Error()
	case res.ExitCode != 0:
		return fmt.Sprintf("exit %d: %s", res.ExitCode, truncate(strings.TrimSpace(string(res.Stderr)+" "+string(res.Stdout)), 300))
	}
	return ""
}

// containerState reports whether a container is running and its exit code.
func (e *dockerWorkspace) containerState(ctx context.Context, name string) (bool, int, error) {
	res, err := e.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}}", name)
	if err != nil {
		return false, 0, fmt.Errorf("inspect: %w", err)
	}
	f := strings.Fields(string(res.Stdout))
	if len(f) != 2 {
		return false, 0, fmt.Errorf("inspect: unexpected output %q", truncate(string(res.Stdout), 100))
	}
	code, err := strconv.Atoi(f[1])
	if err != nil {
		return false, 0, fmt.Errorf("inspect: parse exit code: %w", err)
	}
	return f[0] == "true", code, nil
}

// logTail is the last lines of a container's log, for an error message.
func (e *dockerWorkspace) logTail(ctx context.Context, name string) string {
	res, err := e.cfg.Docker.Run(ctx, "logs", "--tail", "20", name)
	if err != nil {
		return err.Error()
	}
	return truncate(strings.TrimSpace(string(res.Stdout)+"\n"+string(res.Stderr)), 600)
}

// startToolProxy starts the per-run tool proxy on the service network with
// host name toolproxy, its routes in a config volume at /cfg (the tool probe
// layer later copies fault.json there) and its ledger in a volume at /ledger,
// and waits for GET /healthz.
func (e *dockerWorkspace) startToolProxy(ctx context.Context, rc *schemas.RunContext, expID, runID string, routes []toolRoute, probe string, ext []externalAPI, upstreams bool) error {
	image := e.toolProxyImage()
	info, err := InspectImage(ctx, e.cfg.Docker, image)
	if err != nil {
		return fmt.Errorf("tool proxy image (images.toolproxy): %w", err)
	}
	name := toolProxyContainer(runID)
	e.removeContainers(ctx, name)
	ledger, cfgVol := toolProxyLedgerVolume(runID), toolProxyCfgVolume(runID)
	for _, v := range []string{ledger, cfgVol} {
		if err := e.freshVolume(ctx, v, expID, runID); err != nil {
			return err
		}
	}
	if err := e.ownVolumes(ctx, rc, expID, runID, info.User, ledger); err != nil {
		return err
	}
	args := append([]string{"create", "--name", name}, utils.LabelArgs(expID, runID)...)
	args = append(args, "--network", serviceNetwork(runID))
	args = append(args, proxyAliasArgs(rc)...) // Eval integrity area (S2): toolproxy, plus tools.internal in a neutral run (realism.go)
	args = append(args,
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:size=64m",
		"--pids-limit", "256", "--memory", "512m", "--log-driver", "json-file", "--log-opt", "max-size=1m",
		"-v", ledger+":/ledger", "-v", cfgVol+":"+toolProxyCfg)
	args = append(args, externalProxyEnv(ext)...) // External APIs area: credential names only, values from the bench's environment
	args = append(args, "--entrypoint", "bifrost-bench", image,
		"toolproxy", "-listen", ":"+strconv.Itoa(ToolProxyPort), "-routes", toolProxyCfg+"/routes.json",
		"-fault", toolProxyCfg+"/fault.json", "-ledger", toolProxyLedger)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("create tool proxy: %w", err)
	}
	if upstreams { // the proxied services sit on the hidden network only
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", hiddenNetwork(runID), name); err != nil {
			return fmt.Errorf("connect tool proxy to the upstream network: %w", err)
		}
	}
	raw, err := json.Marshal(struct {
		RunID   string            `json:"run_id"`
		Routes  []toolRoute       `json:"routes"`
		Members map[string]string `json:"members,omitempty"` // Multi-agent area: team member route tokens (toolproxy/members.go)
	}{runID, routes, utils.MemberRoutes(rc.TeamRoutes)})
	if err != nil {
		return fmt.Errorf("encode routes: %w", err)
	}
	if err := e.copyBytes(ctx, name, toolProxyCfg, "routes.json", raw); err != nil {
		return fmt.Errorf("write routes: %w", err)
	}
	if err := e.prepareExternal(ctx, name, runID, info.User, ext); err != nil { // External APIs area
		return err
	}
	if _, err := e.cfg.Docker.MustRun(ctx, "start", name); err != nil {
		return fmt.Errorf("start tool proxy: %w", err)
	}
	health := schemas.ServiceSpec{ID: ToolProxyHost, Port: ToolProxyPort, Ready: &schemas.ReadyCheck{HTTP: "/healthz", TimeoutS: toolProxyReadyS}}
	return e.waitReady(ctx, name, health, probe)
}

// ownVolumes hands the roots of volumes to user (a numeric uid[:gid]) so a
// non-root process can write them; for a named user they become 1777.
func (e *dockerWorkspace) ownVolumes(ctx context.Context, rc *schemas.RunContext, expID, runID, user string, vols ...string) error {
	if len(vols) == 0 {
		return nil
	}
	args := e.helperArgs(helperName(runID, "own"), expID, runID, []string{"CHOWN", "FOWNER"})
	var dirs []string
	for i, v := range vols {
		d := "/v/" + strconv.Itoa(i)
		args = append(args, "-v", v+":"+d)
		dirs = append(dirs, d)
	}
	uid, gid, _ := numericOwner(user)
	script := `set -eu
if [ -n "$BENCH_UID" ]; then chown "$BENCH_UID:$BENCH_GID" "$@"; chmod 0700 "$@"; else chmod 1777 "$@"; fi`
	args = append(args, "-e", "BENCH_UID="+uid, "-e", "BENCH_GID="+gid)
	args = append(append(args, e.helperImage(rc), "-c", script, "bench-own"), dirs...)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("prepare volumes: %w", err)
	}
	return nil
}

// exportOne reads one export into the run folder.
func (e *dockerWorkspace) exportOne(ctx context.Context, runID, dir string, spec schemas.ServiceSpec, x schemas.StateExport) (schemas.StateFile, error) {
	rel := path.Join(StateFolder, spec.ID, x.ID)
	f := schemas.StateFile{Service: spec.ID, ID: x.ID, Path: rel}
	out := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return f, fmt.Errorf("create %s: %w", filepath.Dir(out), err)
	}
	limit := x.MaxBytes
	if limit <= 0 {
		limit = defaultStateCap
	}
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	container := serviceContainer(runID, spec.ID)
	var n int64
	var problem string
	var err error
	if len(x.Argv) > 0 {
		n, problem, err = e.exportArgv(cctx, container, x.Argv, out, limit)
	} else {
		n, problem, err = e.exportPath(cctx, container, x.Path, out, limit)
	}
	if err != nil {
		return f, err
	}
	if problem != "" {
		f.Error = problem
		if rerr := os.Remove(out); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return f, fmt.Errorf("remove %s: %w", out, rerr)
		}
		return f, nil
	}
	sum, err := utils.SHA256File(out)
	if err != nil {
		return f, err
	}
	f.SHA256, f.Bytes = sum, n
	return f, nil
}

// exportArgv writes the stdout of argv run in the service container to out.
// It returns the size, or a problem when the command failed or its output
// went over limit.
func (e *dockerWorkspace) exportArgv(ctx context.Context, container string, argv []string, out string, limit int64) (int64, string, error) {
	fh, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", out, err)
	}
	w := &cappedWriter{w: fh, limit: limit}
	var errBuf strings.Builder
	stderr := &cappedWriter{w: &errBuf, limit: 2048}
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), append([]string{"exec", container}, argv...)...)
	cmd.Stdout, cmd.Stderr = w, stderr
	runErr := cmd.Run()
	if err := errors.Join(w.err, fh.Close()); err != nil {
		return 0, "", fmt.Errorf("write %s: %w", out, err)
	}
	switch {
	case ctx.Err() != nil:
		return 0, fmt.Sprintf("export command did not finish in %s", exportTimeout), nil
	case runErr != nil:
		return 0, fmt.Sprintf("export command failed: %v: %s", runErr, truncate(strings.TrimSpace(errBuf.String()), 300)), nil
	case w.truncated:
		return 0, fmt.Sprintf("export is over its %d byte cap", limit), nil
	}
	return w.n, "", nil
}

// exportPath copies a file or folder out of the service container. A regular
// file is stored as its content, anything else as the tar stream docker cp
// produces. The stream is spooled to a temporary file first, capped.
func (e *dockerWorkspace) exportPath(ctx context.Context, container, src, out string, limit int64) (int64, string, error) {
	spool := out + ".tar"
	defer os.Remove(spool)
	fh, err := os.OpenFile(spool, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", spool, err)
	}
	w := &cappedWriter{w: fh, limit: limit + 1<<20}
	var errBuf strings.Builder
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), "cp", container+":"+src, "-")
	cmd.Stdout, cmd.Stderr = w, &errBuf
	runErr := cmd.Run()
	if err := errors.Join(w.err, fh.Close()); err != nil {
		return 0, "", fmt.Errorf("write %s: %w", spool, err)
	}
	switch {
	case runErr != nil:
		return 0, fmt.Sprintf("copy %s failed: %s", src, truncate(strings.TrimSpace(errBuf.String()), 300)), nil
	case w.truncated:
		return 0, fmt.Sprintf("export is over its %d byte cap", limit), nil
	}
	return unpackExport(spool, out, limit)
}

// unpackExport turns a docker cp tar stream into the export: the content of a
// single regular file, else the tar itself.
func unpackExport(spool, out string, limit int64) (int64, string, error) {
	in, err := os.Open(spool)
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", spool, err)
	}
	defer in.Close()
	tr := tar.NewReader(in)
	hdr, err := tr.Next()
	if err != nil {
		return 0, fmt.Sprintf("export stream is not a tar: %v", err), nil
	}
	if hdr.Typeflag == tar.TypeReg {
		if hdr.Size > limit {
			return 0, fmt.Sprintf("export is %d bytes, over its %d byte cap", hdr.Size, limit), nil
		}
		data, err := io.ReadAll(io.LimitReader(tr, limit))
		if err != nil {
			return 0, fmt.Sprintf("read export: %v", err), nil
		}
		if _, err := tr.Next(); err == io.EOF {
			if err := os.WriteFile(out, data, 0o644); err != nil {
				return 0, "", fmt.Errorf("write %s: %w", out, err)
			}
			return int64(len(data)), "", nil
		}
	}
	st, err := os.Stat(spool)
	if err != nil {
		return 0, "", fmt.Errorf("stat %s: %w", spool, err)
	}
	if st.Size() > limit {
		return 0, fmt.Sprintf("export is %d bytes, over its %d byte cap", st.Size(), limit), nil
	}
	if err := os.Rename(spool, out); err != nil {
		return 0, "", fmt.Errorf("move %s: %w", spool, err)
	}
	return st.Size(), "", nil
}

// copyLedger copies the tool proxy's ledger into the run folder as
// tools.jsonl. A missing ledger leaves no file, so tool evidence is unknown.
func (e *dockerWorkspace) copyLedger(ctx context.Context, runID, dir string) error {
	out := filepath.Join(dir, ToolsFile)
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	n, problem, err := e.exportPath(cctx, toolProxyContainer(runID), toolProxyLedger, out, maxLedgerBytes)
	if err != nil {
		return err
	}
	if problem != "" {
		if rerr := os.Remove(out); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return errors.Join(errors.New(problem), rerr)
		}
		return errors.New(problem)
	}
	e.cfg.Logger.Debug("tool ledger copied", "run", runID, "bytes", n)
	return nil
}

// copyFiles copies host files or folders (relative to base, resolved inside
// it) into a created container at their container paths, owned by uid:gid.
// The tar is written by the bench from trusted task files; symlinks inside a
// copied folder are skipped.
func (e *dockerWorkspace) copyFiles(ctx context.Context, container, base string, files map[string]string, uid, gid int) error {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(writeFilesTar(pw, base, files, uid, gid))
	}()
	return e.cpTar(ctx, container, "/", pr)
}

// copyBytes writes one file into a container folder.
func (e *dockerWorkspace) copyBytes(ctx context.Context, container, dir, name string, data []byte) error {
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)})
		if err == nil {
			_, err = tw.Write(data)
		}
		pw.CloseWithError(errors.Join(err, tw.Close()))
	}()
	return e.cpTar(ctx, container, dir, pr)
}

// cpTar feeds a tar stream to `docker cp -a - container:dir`.
func (e *dockerWorkspace) cpTar(ctx context.Context, container, dir string, r io.ReadCloser) error {
	defer r.Close()
	var errBuf strings.Builder
	cmd := exec.CommandContext(ctx, dockerBinary(e.cfg.Docker), "cp", "-a", "-", container+":"+dir)
	cmd.Stdin, cmd.Stderr = r, &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker cp: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// writeFilesTar writes files (container path -> path relative to base) as a
// tar rooted at "/". Missing parents are created by docker cp.
func writeFilesTar(w io.Writer, base string, files map[string]string, uid, gid int) error {
	tw := tar.NewWriter(w)
	for _, dst := range sortedKeys(files) {
		src, err := utils.ResolveInside(base, files[dst])
		if err != nil {
			return fmt.Errorf("file %s: %w", dst, err)
		}
		if err := addTree(tw, src, strings.TrimPrefix(dst, "/"), uid, gid); err != nil {
			return err
		}
	}
	return tw.Close()
}

// addTree adds a host file, or a folder and everything under it, at name.
func addTree(tw *tar.Writer, src, name string, uid, gid int) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		entry := path.Join(name, filepath.ToSlash(rel))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return tw.WriteHeader(&tar.Header{Name: entry + "/", Mode: int64(info.Mode().Perm()), Typeflag: tar.TypeDir, Uid: uid, Gid: gid, ModTime: info.ModTime()})
		case !info.Mode().IsRegular():
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr := &tar.Header{Name: entry, Mode: int64(info.Mode().Perm()), Size: info.Size(), Typeflag: tar.TypeReg, Uid: uid, Gid: gid, ModTime: info.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.CopyN(tw, f, info.Size())
		return err
	})
}

// numericUser returns the uid and gid of an image USER when it is numeric,
// else root. It is pure.
func numericUser(user string) (int, int) {
	u, g, ok := numericOwner(user)
	if !ok {
		return 0, 0
	}
	uid, uerr := strconv.Atoi(u)
	gid, gerr := strconv.Atoi(g)
	if uerr != nil || gerr != nil {
		return 0, 0
	}
	return uid, gid
}

// numericOwner splits a numeric "uid[:gid]" user; the gid defaults to the
// uid. It reports false for a named or empty user. It is pure.
func numericOwner(user string) (string, string, bool) {
	u, g, hasGroup := strings.Cut(user, ":")
	if _, err := strconv.Atoi(u); err != nil {
		return "", "", false
	}
	if !hasGroup {
		g = u
	}
	if _, err := strconv.Atoi(g); err != nil {
		g = u
	}
	return u, g, true
}

// upstreamURL is the service's own address on the run network.
func upstreamURL(s schemas.ServiceSpec) string {
	return "http://" + s.ID + ":" + strconv.Itoa(s.Port)
}

// toolProxyURL is where the agent reaches a proxied service. For an MCP
// service utils.MCPServers appends the MCP path (/mcp by default).
func toolProxyURL(id string) string {
	return "http://" + ToolProxyHost + ":" + strconv.Itoa(ToolProxyPort) + "/s/" + id
}

func serviceNetwork(runID string) string {
	if neutralRun(runID) { // Eval integrity area (S2): its name is the domain reverse DNS answers on it
		return neutralNetworkPrefix + runID
	}
	return "bench-net-" + runID
}

func hiddenNetwork(runID string) string { return "bench-hnet-" + runID }

func serviceContainer(runID, id string) string { return "bench-" + runID + "-svc-" + id }

func toolProxyContainer(runID string) string { return "bench-tp-" + runID }

func toolProxyLedgerVolume(runID string) string { return "bench-tpl-" + runID }

func toolProxyCfgVolume(runID string) string { return "bench-tpc-" + runID }
