// Package environments holds the bench's Environment implementations: the
// isolated workspace an agent runs in, from creation to diff export and
// teardown.
package environments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// DockerWorkspaceType is the part name suites use to select this environment.
const DockerWorkspaceType = "docker_workspace"

const (
	dockerWorkspaceVersion = "1.0.0"
	defaultOutputCap       = 50 << 20
	maxDiffBytes           = 64 << 20
	defaultUser            = "1000"
	defaultPids            = 512
	defaultWallS           = 900
	defaultWorkdir         = "/workspace"
	agentHome              = "/home/agent"
	auxMount               = "/opt/bench"
	shimDir                = auxMount + "/shim"
	defaultPath            = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	staleNotesCondition    = "stale-notes"
	projectFileTemplate    = "{{agent.notes.project_file}}"
	// baselineDate makes the baseline commit id a pure function of the files,
	// so every arm of a task starts from the same BaselineRef.
	baselineDate = "2000-01-01T00:00:00Z"
	killGrace    = 30 * time.Second
)

var (
	runIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	refRe   = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
)

// DockerWorkspaceConfig configures the docker_workspace environment.
// HelperImage runs the trusted copy, commit and export steps and must contain
// git, sh and coreutils (the suite base image does). OutputCapBytes caps each
// captured agent stream; zero means 50 MB.
//
// NetWatch runs a passive network observer next to every agent (`agent
// check` only): the agent joins the network namespace of a holder container
// on the evaluation network instead of attaching to it directly, and an
// observer with only CAP_NET_RAW in that namespace writes what it sees leave
// the agent (DNS names, connection attempts, HTTP request lines and header
// names) to netwatch.jsonl in the run folder. The helper image must have
// python3 and sleep. When the observer cannot start, the agent runs as usual
// and the file records why.
//
// ToolProxyImage (images.toolproxy) runs the per-run tool proxy when a run
// has proxied services; empty means DefaultToolProxyImage.
//
// ProcessWatch and WorkspaceWatch run the trusted step observers beside each
// agent session (observers.go, docs/steps-and-evidence.md): a process
// observer in the agent's PID namespace polling /proc every WatchIntervalMS
// (zero means DefaultWatchIntervalMS), and an inotify observer on the
// workspace volume. The bench config turns both on by default.
type DockerWorkspaceConfig struct {
	EvalNetwork    string
	HelperImage    string
	Logger         schemas.Logger
	Docker         utils.Docker
	OutputCapBytes int64
	NetWatch       bool
	ToolProxyImage string

	// Agent steps area: the trusted step observers.
	ProcessWatch    bool
	WorkspaceWatch  bool
	WatchIntervalMS int

	// Remote coverage: where a remote agent's tool gate is published
	// (remote_tools.listen, remote_tools.public_url; remotetools.go).
	RemoteToolsListen    string
	RemoteToolsPublicURL string

	// Agent events area: the per-run agent event collector, the events file
	// and the OpenTelemetry variables (eventcollector.go).
	EventsCollector bool

	// W3 customer support: services' own gateway access (servicegateway.go).
	ServiceGateway ServiceGatewayConfig
}

// dockerWorkspace keeps each run's workspace in a docker volume and runs every
// step in short-lived containers, so the host never touches agent files.
type dockerWorkspace struct {
	cfg DockerWorkspaceConfig
}

// newDockerWorkspace builds the environment and fills config defaults.
func newDockerWorkspace(cfg DockerWorkspaceConfig) *dockerWorkspace {
	if cfg.Logger == nil {
		cfg.Logger = schemas.NoopLogger{}
	}
	if cfg.OutputCapBytes <= 0 {
		cfg.OutputCapBytes = defaultOutputCap
	}
	return &dockerWorkspace{cfg: cfg}
}

// GetName implements schemas.Part.
func (e *dockerWorkspace) GetName() string { return DockerWorkspaceType }

// GetVersion implements schemas.Part.
func (e *dockerWorkspace) GetVersion() string { return dockerWorkspaceVersion }

// SupportsAdapterFaults reports support for isolated per-run hook controllers.
func (e *dockerWorkspace) SupportsAdapterFaults() bool { return true }

// Create fills a new workspace volume from the trusted task folder, lays down
// the clean notes file (except under the stale-notes condition, whose fixture
// layer writes the stale file), hands every file to uid 1000 and records a
// baseline commit inside the volume. The baseline uses `add --force`, like the
// export, so a repo file its own .gitignore matches never shows up as added.
// Protected hashes come from the host task folder, which is trusted. A task
// whose environment names a workspace path is seeded from that folder of its
// own image instead (built or pulled when missing), its protected hashes are
// read from the seeded volume, and the handle's workdir is that path. A run
// with memory (sessions, or an agent memory section) also gets its memory
// volumes, empty.
func (e *dockerWorkspace) Create(ctx context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Task == nil || rc.Task.Dir == "" {
		return nil, fmt.Errorf("create workspace for run %s: run context has no task folder", runID)
	}
	wp := workspacePath(rc)
	if err := checkWorkspacePath(wp); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	if env := rc.Task.Environment; env != nil && env.User != "" && env.User != utils.TaskUserRoot {
		return nil, fmt.Errorf("create workspace for run %s: environment user %q is not root", runID, env.User)
	}
	var repoDir string
	if wp == "" {
		if repoDir, err = taskRepo(rc); err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
		}
	}
	notesRel, notesHost, err := cleanNotes(rc)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	var protected map[string]string
	if repoDir != "" {
		if protected, err = protectedHashes(repoDir, rc.Task.Protected); err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
		}
	}
	if err := e.checkFrozenImage(ctx, rc); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	view := viewFor(rc) // Eval integrity area (S2): neutral runs get neutral volume names (realism.go)
	h := &schemas.EnvironmentHandle{
		Volume:     view.workspaceVolume(runID), // Eval integrity area (S2): workspaceVolume(runID) in a standard run
		AuxVolume:  view.auxVolume(runID),       // Eval integrity area (S2): auxVolume(runID) in a standard run
		Network:    e.network(""),
		Workdir:    workdir(rc),
		Labels:     labelMap(expID, runID),
		FileHashes: map[string]string{},
		Protected:  protected,
	}
	if wp != "" {
		h.Workdir = wp
	}
	if notesRel != "" {
		sum, err := utils.SHA256File(notesHost)
		if err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
		}
		h.FileHashes[notesRel] = sum
	}
	for _, v := range []string{h.Volume, h.AuxVolume} {
		if err := e.freshVolume(ctx, v, expID, runID); err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
		}
	}
	if err := e.createMemory(ctx, rc, expID, runID); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	ref, err := e.seed(ctx, rc, h, expID, runID, seedOptions{})
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	h.BaselineRef = ref
	if wp != "" {
		if h.Protected, err = e.volumeProtected(ctx, rc, h, expID, runID); err != nil {
			return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
		}
	}
	e.cfg.Logger.Debug("workspace created", "run", runID, "volume", h.Volume, "baseline", ref)
	return h, nil
}

// Exec runs the plan in a hardened container and waits for it. The container
// is created first, so image and flag errors surface as errors, then started
// attached so stdout and stderr stream into capped files in the run folder.
// The wall budget (plan.WallS, 900 s when unset) is a context deadline; on
// expiry the container is killed and TimedOut is set. The container is
// removed, except in a snapshot-graded run, where Snapshot needs it. Root
// users and host, bridge or container networks are refused.
//
// Scenario runs: with started services (rc.Services) the agent also joins the
// run's service network (it is the only network when the plan has none).
// The plan's folders (RunPlan.Dirs) arrive as read-only volumes, the memory
// volumes are mounted, and a task image's workspace path mounts the workspace
// a second time. With rc.Session set the container is bench-<run>-s<n>, the
// earlier sessions' containers are removed first, and the output goes to
// agent.stream.<n>.jsonl and agent.stderr.<n>.txt (ExecResult paths), then is
// appended to agent.stream.jsonl and agent.stderr.txt.
// notLaunched marks an Exec error raised before the agent container started:
// the pipeline may then retry the attempt (schemas.ErrNotLaunched, 2-A-12).
func notLaunched(err error) error {
	return fmt.Errorf("%w (%w)", err, schemas.ErrNotLaunched)
}

func (e *dockerWorkspace) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, notLaunched(err)
	}
	if rc.Handle == nil || rc.Dir == "" || plan == nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: needs a workspace handle, a run folder and a plan", runID))
	}
	root := utils.TaskRunsAsRoot(rc.Task)
	if root {
		// Recorded in the plan the run kernel writes to plan.json.
		plan.User = rootUser
	}
	if err := e.checkPlan(plan, root); err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	basePath, err := e.imagePath(ctx, plan)
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	view := viewFor(rc)                    // Eval integrity area (S2): the names the agent is shown (realism.go)
	agentPlan, err := view.agentPlan(plan) // Eval integrity area (S2): plan itself stays as planned for plan.json
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	agentPlan = withTaskEnv(rc, agentPlan) // X2: the workspace service's environment, under the plan's own variables (compose.go)
	name := agentName(rc, runID)
	e.removeContainers(ctx, staleAgents(rc, runID)...)
	mounts, links, err := e.agentMounts(ctx, rc, agentPlan, expID, runID) // Eval integrity area (S2): agentPlan
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	// X7: the run's GPUs, for the agent container only (gpu.go).
	gpuArgs, err := agentGPUArgs(rc)
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	mounts = append(mounts, gpuArgs...)
	l, err := buildLaunchView(agentPlan, view.shimDir()+":"+basePath, agentHome, links, view) // Eval integrity area (S2): view
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	// Remote area: the one route out a remote_http plan asks for (RunPlan.Egress, utils/remoteegress.go); nil for every other plan.
	egress, err := utils.StartRemoteEgress(ctx, e.cfg.Docker, rc, plan, e.network(plan.Network))
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	defer egress.Stop(context.WithoutCancel(ctx))
	e.ensureEventCollector(ctx, rc, plan, expID, runID) // Agent events area: the collector, started before the first session (eventcollector.go)
	defer e.copyAgentEvents(context.WithoutCancel(ctx), runID, rc.Dir)
	l.Env = e.eventEnvView(rc, runID, l.Env, view)                 // Agent events area: BENCH_EVENTS_* and OTEL_* (eventcollector.go); Eval integrity area (S2): view
	if err := view.writeRealism(rc.Dir, plan, l.Env); err != nil { // Eval integrity area (S2): realism.json, neutral runs only
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	envFile, err := writeEnvFile(l.Env)
	if err != nil {
		return nil, notLaunched(fmt.Errorf("exec run %s: %w", runID, err))
	}
	network, svcNet := e.network(plan.Network), ""
	if len(rc.Services) > 0 || collectorRunning(runID) { // Agent events area: the collector is on the service network
		svcNet = serviceNetwork(runID)
		if network == "none" {
			network, svcNet = svcNet, ""
		}
	}
	joiner := name
	if w := e.netWatch(ctx, rc, expID, runID, network); w != nil {
		defer w.close(ctx)
		network, joiner = "container:"+w.holder, w.holder
	}
	// Agent steps area: trusted process and workspace observers (observers.go); never fail the run.
	obs := e.startObservers(ctx, rc, plan, expID, runID)
	defer obs.finish(ctx, rc, nil)
	args := agentCreateArgsView(name, utils.LabelArgs(expID, runID), agentPlan, rc.Handle, network, envFile, l, mounts, snapshotMode(rc) || root, view) // Eval integrity area (S2): view
	_, cerr := e.cfg.Docker.MustRun(ctx, args...)
	if rerr := os.Remove(envFile); rerr != nil {
		e.cfg.Logger.Warn("remove env file", "path", envFile, "error", rerr)
	}
	// A snapshot-graded run keeps the exited container for Snapshot, which
	// removes it after the commit; Destroy removes it in any case.
	keep := snapshotMode(rc)
	defer func() {
		if !keep {
			e.removeContainers(ctx, name)
		}
	}()
	if cerr != nil {
		keep = false
		return nil, notLaunched(fmt.Errorf("exec run %s: create agent container: %w", runID, cerr))
	}
	if svcNet != "" {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", svcNet, joiner); err != nil {
			keep = false
			return nil, notLaunched(fmt.Errorf("exec run %s: join service network: %w", runID, err))
		}
	}
	if rc.FaultRuntime != nil {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", rc.FaultRuntime.Network, joiner); err != nil {
			keep = false
			return nil, notLaunched(fmt.Errorf("join adapter fault network: %w", err))
		}
	}
	wall := time.Duration(plan.WallS) * time.Second
	if plan.WallS <= 0 {
		wall = defaultWallS * time.Second
	}
	secret := ""
	if rc.Key != nil {
		secret = rc.Key.Value
	}
	stdout, stderr, session := streamPaths(rc)
	faultToken := ""
	if rc.FaultRuntime != nil {
		faultToken = rc.FaultRuntime.Token
	}

	obs.watchAgent(ctx, rc, expID, runID, name)
	gpuSampler := startGPUSampler(ctx, rc) // X7: host-side nvidia-smi samples of the run's GPUs (gpu.go)
	res, err := e.attach(ctx, name, stdout, stderr, wall, secret, faultToken)
	stopGPUSampler(rc, gpuSampler) // X7
	obs.finish(ctx, rc, res)
	if err != nil {
		keep = false
		return res, fmt.Errorf("exec run %s: %w", runID, err)
	}
	if session > 0 {
		if err := appendSession(rc.Dir, session, stdout, stderr); err != nil {
			return res, fmt.Errorf("exec run %s: %w", runID, err)
		}
	}
	return res, nil
}

// netWatch starts the network observer when NetWatch is on and the agent has
// a network. A failure is recorded in the run folder and returns nil, so the
// agent still runs, on the evaluation network itself.
func (e *dockerWorkspace) netWatch(ctx context.Context, rc *schemas.RunContext, expID, runID, network string) *netWatch {
	if !e.cfg.NetWatch || network == "none" {
		return nil
	}
	w, err := e.startNetWatch(ctx, rc, expID, runID, network)
	if err == nil {
		return w
	}
	e.cfg.Logger.Warn("network observer did not start; the agent runs without it", "run", runID, "error", err.Error())
	if werr := writeNetWatchError(rc.Dir, err); werr != nil {
		e.cfg.Logger.Warn("record network observer failure", "run", runID, "error", werr.Error())
	}
	return nil
}

// ExportDiff copies the workspace to /tmp inside a trusted helper with no
// network and the volume read-only, and diffs it against the baseline there.
// The copy's git config, index and info folder are replaced before git runs,
// because the agent controls them: a planted filter, fsmonitor command or
// assume-unchanged bit could otherwise run code or hide a change. `--force`
// exports output files a .gitignore excludes. The allowlist then runs in Go.
func (e *dockerWorkspace) ExportDiff(ctx context.Context, rc *schemas.RunContext) (*schemas.Diff, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" {
		return nil, fmt.Errorf("export diff for run %s: needs a workspace handle and a run folder", runID)
	}
	if !refRe.MatchString(rc.Handle.BaselineRef) {
		return nil, fmt.Errorf("export diff for run %s: invalid baseline ref %q", runID, rc.Handle.BaselineRef)
	}
	args := e.helperArgs(helperName(runID, "export"), expID, runID, []string{"DAC_OVERRIDE", "DAC_READ_SEARCH"})
	args = append(args,
		"-v", rc.Handle.Volume+":/workspace:ro",
		"-e", "BENCH_BASE="+rc.Handle.BaselineRef,
		"-e", "BENCH_CAP="+strconv.Itoa(maxDiffBytes),
		e.helperImage(rc), "-c", exportScript)
	res, err := e.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", runID, err)
	}
	f, err := filterDiff(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("export diff for run %s: %w", runID, err)
	}
	for _, r := range f.Rejected {
		if r.Reason == RejectCache {
			e.cfg.Logger.Debug("diff path dropped", "run", runID, "path", r.Path, "reason", r.Reason)
			continue
		}
		e.cfg.Logger.Warn("diff path rejected", "run", runID, "path", r.Path, "reason", r.Reason)
	}
	out := filepath.Join(rc.Dir, "workspace.diff")
	if err := os.WriteFile(out, f.Kept, 0o644); err != nil {
		return nil, fmt.Errorf("export diff for run %s: write %s: %w", runID, out, err)
	}
	return &schemas.Diff{Path: out, SHA256: utils.SHA256Bytes(f.Kept), Files: f.Files, Rejected: f.Rejected}, nil
}

// Destroy removes every container, volume, network and image labelled with
// the run (services, the tool proxy, the snapshot image), plus the run's
// volumes by name. It is idempotent and works after a failed Create.
func (e *dockerWorkspace) Destroy(ctx context.Context, rc *schemas.RunContext) error {
	_, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	e.revokeServiceKeys(ctx, runID) // W3 customer support: any service key still live (servicegateway.go)
	defer forgetCollector(runID)    // Agent events area: the run's collector bookkeeping ends with the run (eventcollector.go)
	filter := "label=" + utils.LabelRun + "=" + runID
	var errs []error
	ps, err := e.cfg.Docker.MustRun(ctx, "ps", "-aq", "--filter", filter)
	if err != nil {
		errs = append(errs, err)
	} else if ids := strings.Fields(string(ps.Stdout)); len(ids) > 0 {
		if _, err := e.cfg.Docker.MustRun(ctx, append([]string{"rm", "-f", "-v"}, ids...)...); err != nil {
			errs = append(errs, err)
		}
	}
	vols := map[string]bool{workspaceVolume(runID): true, auxVolume(runID): true}
	vols[viewFor(rc).workspaceVolume(runID)], vols[viewFor(rc).auxVolume(runID)] = true, true // Eval integrity area (S2): a neutral run's names
	if rc.Handle != nil {
		for _, v := range []string{rc.Handle.Volume, rc.Handle.AuxVolume} {
			if v != "" {
				vols[v] = true
			}
		}
	}
	if memoryEnabled(rc) {
		// Named even when Create failed before labelling them: docker
		// creates a missing named volume, unlabelled, on first mount. Paths
		// that do not validate never got volumes, so they are not counted.
		paths, perr := memoryPaths(rc)
		if perr != nil {
			paths = nil
		}
		for _, v := range viewFor(rc).memoryVolumes(runID, len(paths)) { // Eval integrity area (S2): the run's view names them
			vols[v] = true
		}
	}
	ls, err := e.cfg.Docker.MustRun(ctx, "volume", "ls", "-q", "--filter", filter)
	if err != nil {
		errs = append(errs, err)
	} else {
		for _, v := range strings.Fields(string(ls.Stdout)) {
			vols[v] = true
		}
	}
	names := make([]string, 0, len(vols))
	for v := range vols {
		names = append(names, v)
	}
	sort.Strings(names)
	if _, err := e.cfg.Docker.MustRun(ctx, append([]string{"volume", "rm", "-f"}, names...)...); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, e.removeLabelled(ctx, filter, []string{"network", "ls", "-q"}, []string{"network", "rm"}))
	errs = append(errs, e.removeLabelled(ctx, filter, []string{"image", "ls", "-q"}, []string{"image", "rm", "-f"}))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("destroy run %s: %w", runID, err)
	}
	neutralRuns.Delete(runID) // Eval integrity area (S2): everything named after the run is gone
	return nil
}

// removeLabelled lists objects with list plus the run filter and removes
// them with rm. Nothing listed is nothing to do.
func (e *dockerWorkspace) removeLabelled(ctx context.Context, filter string, list, rm []string) error {
	res, err := e.cfg.Docker.MustRun(ctx, append(list, "--filter", filter)...)
	if err != nil {
		return err
	}
	ids := uniqueFields(res.Stdout)
	if len(ids) == 0 {
		return nil
	}
	_, err = e.cfg.Docker.MustRun(ctx, append(rm, ids...)...)
	return err
}

// uniqueFields splits a listing into its distinct ids, in order. It is pure.
func uniqueFields(out []byte) []string {
	var ids []string
	seen := map[string]bool{}
	for _, id := range strings.Fields(string(out)) {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// exportScript runs in the trusted helper over agent-written files. It works
// on a copy, resets every git file that can carry behaviour, and caps the
// patch size before printing it.
const exportScript = `set -eu
mkdir /tmp/w
cp -dR --preserve=mode,timestamps /workspace/. /tmp/w/
cd /tmp/w
if [ -L .git ] || [ ! -d .git ]; then echo "workspace has no .git folder" >&2; exit 3; fi
rm -rf .git/index .git/index.lock .git/info .git/hooks .git/config.worktree .git/commondir
printf '[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n' > .git/config
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp GIT_TERMINAL_PROMPT=0
g() { git -c core.hooksPath=/dev/null -c diff.external= -c core.fsmonitor=false -c core.autocrlf=false -c core.quotepath=true "$@"; }
g cat-file -e "$BENCH_BASE^{commit}" || { echo "baseline commit $BENCH_BASE not found" >&2; exit 3; }
g add -A --force
g diff --cached --binary --no-ext-diff --no-textconv --no-color --no-renames --src-prefix=a/ --dst-prefix=b/ "$BENCH_BASE" > /tmp/out.diff
size="$(wc -c < /tmp/out.diff)"
if [ "$size" -gt "$BENCH_CAP" ]; then echo "diff is $size bytes, over the $BENCH_CAP byte cap" >&2; exit 4; fi
cat /tmp/out.diff
`

// helperArgs is the common prefix of every trusted helper run: removed on
// exit, labelled, no network, root with only the listed capabilities.
func (e *dockerWorkspace) helperArgs(name, expID, runID string, caps []string) []string {
	args := []string{"run", "--rm", "--name", name}
	args = append(args, utils.LabelArgs(expID, runID)...)
	args = append(args, "--network", "none", "--user", "0", "--cap-drop", "ALL")
	for _, c := range caps {
		args = append(args, "--cap-add", c)
	}
	return append(args, "--security-opt", "no-new-privileges", "--pids-limit", "256",
		"--memory", "2g", "--log-driver", "none", "--entrypoint", "/bin/sh")
}

// freshVolume replaces any leftover volume of the same name with a new,
// labelled, empty one.
func (e *dockerWorkspace) freshVolume(ctx context.Context, name, expID, runID string) error {
	if _, err := e.cfg.Docker.MustRun(ctx, "volume", "rm", "-f", name); err != nil {
		return fmt.Errorf("remove stale volume %s: %w", name, err)
	}
	args := append([]string{"volume", "create"}, utils.LabelArgs(expID, runID)...)
	if _, err := e.cfg.Docker.MustRun(ctx, append(args, name)...); err != nil {
		return fmt.Errorf("create volume %s: %w", name, err)
	}
	return nil
}

// checkPlan refuses plans that would break the security baseline. A root
// user is accepted only for a task that runs as root (allowRoot).
func (e *dockerWorkspace) checkPlan(plan *schemas.RunPlan, allowRoot bool) error {
	if plan.Image == "" {
		return errors.New("plan has no image")
	}
	user := plan.User
	if user == "" {
		user = defaultUser
	}
	if isRoot(user) && !allowRoot {
		return fmt.Errorf("plan user %q is root; agents run as non-root", plan.User)
	}
	n := e.network(plan.Network)
	switch {
	case n == "host" || n == "bridge" || n == "default" || strings.HasPrefix(n, "container:"):
		return fmt.Errorf("plan network %q is not allowed; agents use the evaluation network", n)
	case n != "none" && e.cfg.EvalNetwork != "" && n != e.cfg.EvalNetwork:
		return fmt.Errorf("plan network %q is not the evaluation network %q", n, e.cfg.EvalNetwork)
	}
	return nil
}

// imagePath returns the PATH the plan or the image sets, so the shim folder
// can be prepended to it.
func (e *dockerWorkspace) imagePath(ctx context.Context, plan *schemas.RunPlan) (string, error) {
	if p := plan.Env["PATH"]; p != "" {
		return p, nil
	}
	res, err := e.cfg.Docker.MustRun(ctx, "image", "inspect", "--format", "{{json .Config.Env}}", plan.Image)
	if err != nil {
		return "", fmt.Errorf("inspect image %s: %w", plan.Image, err)
	}
	var env []string
	if err := json.Unmarshal(res.Stdout, &env); err != nil {
		return "", fmt.Errorf("inspect image %s: parse env: %w", plan.Image, err)
	}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok && v != "" {
			return v, nil
		}
	}
	return defaultPath, nil
}

// attach starts the created container attached, streams its output into the
// run folder and enforces the wall budget.
func (e *dockerWorkspace) attach(ctx context.Context, name, stdoutPath, stderrPath string, wall time.Duration, secrets ...string) (*schemas.ExecResult, error) {
	res := &schemas.ExecResult{StdoutPath: stdoutPath, StderrPath: stderrPath}
	outF, err := os.OpenFile(res.StdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", res.StdoutPath, err)
	}
	defer outF.Close()
	errF, err := os.OpenFile(res.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", res.StderrPath, err)
	}
	defer errF.Close()
	stdout := &cappedWriter{w: newRedactingWriter(outF, secrets...), limit: e.cfg.OutputCapBytes}
	stderr := &cappedWriter{w: newRedactingWriter(errF, secrets...), limit: e.cfg.OutputCapBytes}
	cmd := exec.Command(dockerBinary(e.cfg.Docker), "start", "-a", name)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	res.Started = time.Now().UTC()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent container: %w", err)
	}
	res.TimedOut = e.waitOrKill(ctx, cmd, name, wall)
	res.Ended = time.Now().UTC()
	res.Truncated = stdout.truncated || stderr.truncated
	if err := firstErr(stdout.err, stderr.err, stdout.w.(*redactingWriter).Flush(), stderr.w.(*redactingWriter).Flush(), outF.Sync(), errF.Sync()); err != nil {
		return res, fmt.Errorf("capture agent output: %w", err)
	}
	if ctx.Err() != nil {
		return res, fmt.Errorf("agent run interrupted: %w", ctx.Err())
	}
	code, err := e.exitCode(ctx, name)
	if err != nil {
		return res, err
	}
	res.ExitCode = code
	return res, nil
}

// redactingWriter removes known credentials before bytes reach run artifacts.
// It buffers the capped stream so credentials split across writes are hidden.
type redactingWriter struct {
	w       io.Writer
	secrets [][]byte
	pending []byte
}

func newRedactingWriter(w io.Writer, secrets ...string) io.Writer {
	rw := &redactingWriter{w: w}
	for _, secret := range secrets {
		if secret != "" {
			rw.secrets = append(rw.secrets, []byte(secret))
		}
	}
	return rw
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	if len(w.secrets) == 0 {
		return w.w.Write(p)
	}
	w.pending = append(w.pending, p...)
	return len(p), nil
}

func (w *redactingWriter) Flush() error {
	if len(w.pending) == 0 {
		return nil
	}
	data := w.pending
	for _, secret := range w.secrets {
		data = bytes.ReplaceAll(data, secret, []byte("[redacted]"))
	}
	_, err := w.w.Write(data)
	w.pending = nil
	return err
}

// waitOrKill waits for the attached CLI, killing the container when the wall
// budget or ctx ends. It reports whether the wall budget expired.
func (e *dockerWorkspace) waitOrKill(ctx context.Context, cmd *exec.Cmd, name string, wall time.Duration) bool {
	done := make(chan struct{})
	go func() {
		if err := cmd.Wait(); err != nil {
			e.cfg.Logger.Debug("docker start -a ended", "container", name, "error", err)
		}
		close(done)
	}()
	wctx, cancel := context.WithTimeout(ctx, wall)
	defer cancel()
	select {
	case <-done:
		return false
	case <-wctx.Done():
	}
	timedOut := ctx.Err() == nil
	kctx, kcancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer kcancel()
	if _, err := e.cfg.Docker.Run(kctx, "kill", name); err != nil {
		e.cfg.Logger.Warn("kill agent container", "container", name, "error", err)
	}
	select {
	case <-done:
	case <-kctx.Done():
		if err := cmd.Process.Kill(); err != nil {
			e.cfg.Logger.Warn("kill docker client", "container", name, "error", err)
		}
		<-done
	}
	return timedOut
}

// exitCode reads the container's exit code from docker, which is more
// reliable than the attached client's own status. A container that never
// started, or that is not in the exited state, has no trustworthy exit code:
// its zero initial value would otherwise be scored as an agent result, so it
// is reported as an infrastructure error instead.
func (e *dockerWorkspace) exitCode(ctx context.Context, name string) (int, error) {
	res, err := e.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{.State.ExitCode}} {{.State.Status}} {{.State.StartedAt}}", name)
	if err != nil {
		return 0, fmt.Errorf("inspect agent container: %w", err)
	}
	return parseAgentState(string(res.Stdout))
}

// parseAgentState parses "<exit code> <status> <started at>" from inspect.
func parseAgentState(out string) (int, error) {
	f := strings.Fields(out)
	if len(f) != 3 {
		return 0, fmt.Errorf("inspect agent container: unexpected state %q", strings.TrimSpace(out))
	}
	code, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, fmt.Errorf("inspect agent container: parse exit code: %w", err)
	}
	if started, err := time.Parse(time.RFC3339Nano, f[2]); err != nil || started.Year() <= 1 {
		return 0, fmt.Errorf("agent container never started (status %s, started_at %s)", f[1], f[2])
	}
	if f[1] != "exited" {
		return 0, fmt.Errorf("agent container did not exit (status %s)", f[1])
	}
	return code, nil
}

// removeContainers force-removes containers by name, ignoring ctx so cleanup
// still happens after a cancellation.
func (e *dockerWorkspace) removeContainers(ctx context.Context, names ...string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), killGrace)
	defer cancel()
	res, err := e.cfg.Docker.Run(cctx, append([]string{"rm", "-f", "-v"}, names...)...)
	if err != nil {
		e.cfg.Logger.Warn("remove containers", "containers", names, "error", err)
		return
	}
	if res.ExitCode != 0 && !strings.Contains(string(res.Stderr), "No such container") {
		e.cfg.Logger.Warn("remove containers", "containers", names, "stderr", strings.TrimSpace(string(res.Stderr)))
	}
}

// helperImage is the configured helper image, else the suite base image.
func (e *dockerWorkspace) helperImage(rc *schemas.RunContext) string {
	if e.cfg.HelperImage == "" && rc.Suite != nil {
		return rc.Suite.BaseImage.Tag
	}
	return e.cfg.HelperImage
}

// network picks the plan's network, then the evaluation network, then none.
func (e *dockerWorkspace) network(planNetwork string) string {
	switch {
	case planNetwork != "":
		return planNetwork
	case e.cfg.EvalNetwork != "":
		return e.cfg.EvalNetwork
	default:
		return "none"
	}
}

// agentCreateArgs is the `docker create` command line for the agent container
// under the security baseline. writableRoot (snapshot-graded runs and root
// tasks) leaves the root file system writable and /tmp in it, because the
// agent's changes there are what the snapshot freezes and a package install
// writes there; the home folder stays a tmpfs, so the agent's own config and
// credentials are never part of the snapshot. A root user (a task with
// environment.user root) gets back the capabilities a package install needs
// (utils.RootCaps), never more; no-new-privileges still holds. It is pure.
func agentCreateArgs(name string, labels []string, plan *schemas.RunPlan, h *schemas.EnvironmentHandle, network, envFile string, l *launch, mounts []string, writableRoot bool) []string {
	return agentCreateArgsView(name, labels, plan, h, network, envFile, l, mounts, writableRoot, standardView)
}

// agentCreateArgsView is agentCreateArgs with the aux volume mounted where the
// run's view puts it (realism.go). The standard view gives exactly
// agentCreateArgs.
func agentCreateArgsView(name string, labels []string, plan *schemas.RunPlan, h *schemas.EnvironmentHandle, network, envFile string, l *launch, mounts []string, writableRoot bool, v agentView) []string { // Eval integrity area (S2)
	user := plan.User
	if user == "" {
		user = defaultUser
	}
	pids := plan.Pids
	if pids <= 0 {
		pids = defaultPids
	}
	wd := plan.Workdir
	if wd == "" {
		wd = defaultWorkdir
	}
	args := []string{"create", "--name", name}
	args = append(args, labels...)
	args = append(args,
		"--user", user,
		"--cap-drop", "ALL",
	)
	if isRoot(user) {
		for _, c := range utils.RootCaps {
			args = append(args, "--cap-add", c)
		}
	}
	args = append(args,
		"--security-opt", "no-new-privileges",
		"--pids-limit", strconv.Itoa(pids),
	)
	if plan.MemGB > 0 {
		mem := strconv.FormatInt(int64(plan.MemGB*(1<<30)), 10)
		args = append(args, "--memory", mem, "--memory-swap", mem)
	}
	if plan.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(plan.CPUs, 'f', -1, 64))
	}
	if !writableRoot {
		args = append(args, "--read-only", "--tmpfs", "/tmp:exec,size=512m")
	}
	args = append(args,
		"--tmpfs", agentHome+":"+homeTmpfsOpts(user),
		"--network", network,
		"-v", h.Volume+":/workspace",
		"-v", h.AuxVolume+":"+v.auxMount, // Eval integrity area (S2): v.auxMount is auxMount in a standard run
	)
	args = append(args, mounts...)
	args = append(args,
		"--log-driver", "none",
		"-w", wd,
		"--env-file", envFile,
	)
	if l.Entrypoint != "" {
		args = append(args, "--entrypoint", l.Entrypoint)
	}
	args = append(args, plan.Image)
	return append(args, l.Args...)
}

// homeTmpfsOpts makes the home tmpfs owned by a numeric user so the agent can
// write its config there.
func homeTmpfsOpts(user string) string {
	opts := "exec,size=512m,mode=0700"
	u, g, hasGroup := strings.Cut(user, ":")
	if _, err := strconv.Atoi(u); err != nil {
		return "exec,size=512m,mode=1777"
	}
	if !hasGroup {
		g = u
	}
	if _, err := strconv.Atoi(g); err != nil {
		g = u
	}
	return opts + ",uid=" + u + ",gid=" + g
}

// cleanNotes returns where the clean notes file goes in the workspace and the
// host file it comes from, or empty strings when nothing is laid down. The
// host file is the real path inside the task folder, resolved right before
// it is bind-mounted, so a notes file that is (or became) a symlink to a
// host file outside the task is refused.
func cleanNotes(rc *schemas.RunContext) (string, string, error) {
	if rc.Agent == nil || rc.Agent.Notes.ProjectFile == "" || rc.Task.Notes.Clean == "" || staleNotes(rc) {
		return "", "", nil
	}
	rel := rc.Agent.Notes.ProjectFile
	if path.IsAbs(rel) || path.Clean(rel) != rel || outsideWorkspace(rel) || rel == "." {
		return "", "", fmt.Errorf("notes project file %q must be a clean path inside the workspace", rel)
	}
	host, err := utils.ResolveInside(rc.Task.Dir, rc.Task.Notes.Clean)
	if err != nil {
		return "", "", fmt.Errorf("task notes.clean: %w", err)
	}
	return rel, host, nil
}

// staleNotes reports whether a fixture probe will overlay the project notes
// file, in which case Create must not lay the clean one down.
func staleNotes(rc *schemas.RunContext) bool {
	if rc.Slot != nil && rc.Slot.Condition == staleNotesCondition {
		return true
	}
	p := rc.Probe
	if p == nil {
		return false
	}
	if p.ID == staleNotesCondition {
		return true
	}
	if p.Layer != schemas.LayerFixture || p.Apply.Overlay == nil {
		return false
	}
	to := p.Apply.Overlay.To
	return to == projectFileTemplate || (rc.Agent != nil && to == rc.Agent.Notes.ProjectFile)
}

// protectedHashes hashes every repo file matching one of globs.
func protectedHashes(repoDir string, globs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(globs) == 0 {
		return out, nil
	}
	all, err := utils.HashFiles(repoDir)
	if err != nil {
		return nil, fmt.Errorf("hash protected files: %w", err)
	}
	for p, sum := range all {
		for _, g := range globs {
			if matchGlob(g, p) {
				out[p] = sum
				break
			}
		}
	}
	return out, nil
}

// matchGlob matches a slash-separated path against a glob where "**" matches
// any number of whole segments and other segments use path.Match.
func matchGlob(pattern, name string) bool {
	return matchSegments(strings.Split(strings.Trim(pattern, "/"), "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], name[0]); err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// runIDs returns the experiment and run ids that label every docker object.
func runIDs(rc *schemas.RunContext) (string, string, error) {
	if rc == nil || rc.Run == nil || rc.Run.RunID == "" {
		return "", "", errors.New("run context has no run id")
	}
	runID := rc.Run.RunID
	if !runIDRe.MatchString(runID) {
		return "", "", fmt.Errorf("run id %q is not usable in docker names", runID)
	}
	expID := rc.Run.ExpID
	if expID == "" && rc.Slot != nil {
		expID = rc.Slot.ExpID
	}
	if expID == "" && rc.Experiment != nil {
		expID = rc.Experiment.ID
	}
	markRealism(rc, runID) // Eval integrity area (S2): name helpers that take only a run id follow the run's realism (realism.go)
	return expID, runID, nil
}

func labelMap(expID, runID string) map[string]string {
	m := map[string]string{utils.LabelBench: "1", utils.LabelRun: runID}
	if expID != "" {
		m[utils.LabelExp] = expID
	}
	if p := utils.Project(); p != "" && runID != "" {
		m[utils.LabelProject] = p
	}
	return m
}

func workdir(rc *schemas.RunContext) string {
	if rc.Suite != nil && rc.Suite.Workdir != "" {
		return rc.Suite.Workdir
	}
	return defaultWorkdir
}

func dockerBinary(d utils.Docker) string {
	if d.Binary != "" {
		return d.Binary
	}
	return "docker"
}

// rootUser is the plan user of a root task.
const rootUser = "0:0"

// isRoot reports whether a docker --user value is root. It is pure.
func isRoot(user string) bool {
	u, _, _ := strings.Cut(user, ":")
	return u == "0" || u == "root"
}

func workspaceVolume(runID string) string { return "bench-ws-" + runID }

func auxVolume(runID string) string { return "bench-aux-" + runID }

func agentContainer(runID string) string {
	if neutralRun(runID) { // Eval integrity area (S2): its name is what reverse DNS answers for the agent's address
		return neutralAgentPrefix + runID
	}
	return "bench-" + runID
}

func helperName(runID, step string) string {
	return "bench-" + runID + "-" + step + "-" + strings.TrimPrefix(utils.NewID("h", 6), "h_")
}
