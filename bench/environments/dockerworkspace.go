// Package environments holds the bench's Environment implementations: the
// isolated workspace an agent runs in, from creation to diff export and
// teardown.
package environments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
type DockerWorkspaceConfig struct {
	EvalNetwork    string
	HelperImage    string
	Logger         schemas.Logger
	Docker         utils.Docker
	OutputCapBytes int64
	NetWatch       bool
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

// Create fills a new workspace volume from the trusted task folder, lays down
// the clean notes file (except under the stale-notes condition, whose fixture
// layer writes the stale file), hands every file to uid 1000 and records a
// baseline commit inside the volume. The baseline uses `add --force`, like the
// export, so a repo file its own .gitignore matches never shows up as added.
// Protected hashes come from the host task folder, which is trusted.
func (e *dockerWorkspace) Create(ctx context.Context, rc *schemas.RunContext) (*schemas.EnvironmentHandle, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Task == nil || rc.Task.Dir == "" {
		return nil, fmt.Errorf("create workspace for run %s: run context has no task folder", runID)
	}
	// Resolved right before the bind mount: docker follows symlinks, so a
	// repo folder swapped for a link after the suite loaded must not mount
	// a host folder outside the task.
	repoDir, err := utils.ResolveInside(rc.Task.Dir, rc.Task.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: task repo: %w", runID, err)
	}
	if st, err := os.Stat(repoDir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("create workspace for run %s: task repo %s is not a folder", runID, repoDir)
	}
	notesRel, notesHost, err := cleanNotes(rc)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	protected, err := protectedHashes(repoDir, rc.Task.Protected)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	if err := e.checkFrozenImage(ctx, rc); err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	h := &schemas.EnvironmentHandle{
		Volume:     workspaceVolume(runID),
		AuxVolume:  auxVolume(runID),
		Network:    e.network(""),
		Workdir:    workdir(rc),
		Labels:     labelMap(expID, runID),
		FileHashes: map[string]string{},
		Protected:  protected,
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
	ref, err := e.populate(ctx, e.helperImage(rc), h, expID, runID, repoDir, notesRel, notesHost)
	if err != nil {
		return nil, fmt.Errorf("create workspace for run %s: %w", runID, err)
	}
	h.BaselineRef = ref
	e.cfg.Logger.Debug("workspace created", "run", runID, "volume", h.Volume, "baseline", ref)
	return h, nil
}

// Exec runs the plan in a hardened container and waits for it. The container
// is created first, so image and flag errors surface as errors, then started
// attached so stdout and stderr stream into capped files in the run folder.
// The wall budget (plan.WallS, 900 s when unset) is a context deadline; on
// expiry the container is killed and TimedOut is set. The container is always
// removed. Root users and host, bridge or container networks are refused.
func (e *dockerWorkspace) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" || plan == nil {
		return nil, fmt.Errorf("exec run %s: needs a workspace handle, a run folder and a plan", runID)
	}
	if err := e.checkPlan(plan); err != nil {
		return nil, fmt.Errorf("exec run %s: %w", runID, err)
	}
	basePath, err := e.imagePath(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", runID, err)
	}
	l, err := buildLaunch(plan, shimDir+":"+basePath, agentHome)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", runID, err)
	}
	envFile, err := writeEnvFile(l.Env)
	if err != nil {
		return nil, fmt.Errorf("exec run %s: %w", runID, err)
	}
	name := agentContainer(runID)
	e.removeContainers(ctx, name)
	network := e.network(plan.Network)
	if w := e.netWatch(ctx, rc, expID, runID, network); w != nil {
		defer w.close(ctx)
		network = "container:" + w.holder
	}
	args := agentCreateArgs(name, utils.LabelArgs(expID, runID), plan, rc.Handle, network, envFile, l)
	_, cerr := e.cfg.Docker.MustRun(ctx, args...)
	if rerr := os.Remove(envFile); rerr != nil {
		e.cfg.Logger.Warn("remove env file", "path", envFile, "error", rerr)
	}
	defer e.removeContainers(ctx, name)
	if cerr != nil {
		return nil, fmt.Errorf("exec run %s: create agent container: %w", runID, cerr)
	}
	wall := time.Duration(plan.WallS) * time.Second
	if plan.WallS <= 0 {
		wall = defaultWallS * time.Second
	}
	res, err := e.attach(ctx, name, rc.Dir, wall)
	if err != nil {
		return res, fmt.Errorf("exec run %s: %w", runID, err)
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

// Destroy removes every container and volume labelled with the run, plus the
// run's volumes by name. It is idempotent and works after a failed Create.
func (e *dockerWorkspace) Destroy(ctx context.Context, rc *schemas.RunContext) error {
	_, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
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
	if rc.Handle != nil {
		for _, v := range []string{rc.Handle.Volume, rc.Handle.AuxVolume} {
			if v != "" {
				vols[v] = true
			}
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
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("destroy run %s: %w", runID, err)
	}
	return nil
}

// populateScript runs as root in the trusted helper. Nothing in the volume is
// agent-written yet, so running git here is safe; hooks are still disabled.
const populateScript = `set -eu
if [ -n "$(ls -A /workspace)" ]; then echo "workspace volume is not empty" >&2; exit 3; fi
# A new volume inherits the image's /workspace owner; git as root refuses a
# repo it does not own, so hand everything to uid 1000 only after the commit.
chown 0:0 /workspace
cp -dR --preserve=mode,timestamps /bench-src/. /workspace/
if [ -n "${BENCH_NOTES:-}" ]; then
	mkdir -p "$(dirname "/workspace/$BENCH_NOTES")"
	cp /bench-in/notes "/workspace/$BENCH_NOTES"
fi
mkdir -p /opt/bench/shim /opt/bench/log
cd /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp
export GIT_AUTHOR_DATE="$BENCH_DATE" GIT_COMMITTER_DATE="$BENCH_DATE"
git -c init.defaultBranch=main init -q
git -c core.hooksPath=/dev/null add -A --force
git -c core.hooksPath=/dev/null -c user.name=bench -c user.email=bench@local -c commit.gpgsign=false commit -q --no-verify --allow-empty -m baseline
ref="$(git rev-parse HEAD)"
chown -R 1000:1000 /workspace /opt/bench
echo "$ref"
`

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

// populate runs populateScript and returns the baseline commit id.
func (e *dockerWorkspace) populate(ctx context.Context, image string, h *schemas.EnvironmentHandle, expID, runID, repoDir, notesRel, notesHost string) (string, error) {
	args := e.helperArgs(helperName(runID, "prep"), expID, runID, []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"})
	args = append(args,
		"-v", h.Volume+":/workspace",
		"-v", h.AuxVolume+":"+auxMount,
		"-v", repoDir+":/bench-src:ro",
		"-e", "BENCH_DATE="+baselineDate)
	if notesRel != "" {
		args = append(args, "-v", notesHost+":/bench-in/notes:ro", "-e", "BENCH_NOTES="+notesRel)
	}
	args = append(args, image, "-c", populateScript)
	res, err := e.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("populate workspace: %w", err)
	}
	lines := strings.Fields(string(res.Stdout))
	if len(lines) == 0 || !refRe.MatchString(lines[len(lines)-1]) {
		return "", fmt.Errorf("populate workspace: no baseline commit id in helper output %q", truncate(string(res.Stdout), 200))
	}
	return lines[len(lines)-1], nil
}

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

// checkPlan refuses plans that would break the security baseline.
func (e *dockerWorkspace) checkPlan(plan *schemas.RunPlan) error {
	if plan.Image == "" {
		return errors.New("plan has no image")
	}
	user := plan.User
	if user == "" {
		user = defaultUser
	}
	if u, _, _ := strings.Cut(user, ":"); u == "0" || u == "root" {
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
func (e *dockerWorkspace) attach(ctx context.Context, name, dir string, wall time.Duration) (*schemas.ExecResult, error) {
	res := &schemas.ExecResult{
		StdoutPath: filepath.Join(dir, "agent.stream.jsonl"),
		StderrPath: filepath.Join(dir, "agent.stderr.txt"),
	}
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
	stdout := &cappedWriter{w: outF, limit: e.cfg.OutputCapBytes}
	stderr := &cappedWriter{w: errF, limit: e.cfg.OutputCapBytes}
	cmd := exec.Command(dockerBinary(e.cfg.Docker), "start", "-a", name)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	res.Started = time.Now().UTC()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start agent container: %w", err)
	}
	res.TimedOut = e.waitOrKill(ctx, cmd, name, wall)
	res.Ended = time.Now().UTC()
	res.Truncated = stdout.truncated || stderr.truncated
	if err := firstErr(stdout.err, stderr.err, outF.Sync(), errF.Sync()); err != nil {
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
// reliable than the attached client's own status.
func (e *dockerWorkspace) exitCode(ctx context.Context, name string) (int, error) {
	res, err := e.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{.State.ExitCode}}", name)
	if err != nil {
		return 0, fmt.Errorf("inspect agent container: %w", err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if err != nil {
		return 0, fmt.Errorf("inspect agent container: parse exit code: %w", err)
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
// under the security baseline. It is pure.
func agentCreateArgs(name string, labels []string, plan *schemas.RunPlan, h *schemas.EnvironmentHandle, network, envFile string, l *launch) []string {
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
	args = append(args,
		"--read-only",
		"--tmpfs", "/tmp:exec,size=512m",
		"--tmpfs", agentHome+":"+homeTmpfsOpts(user),
		"--network", network,
		"-v", h.Volume+":/workspace",
		"-v", h.AuxVolume+":"+auxMount,
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
	return expID, runID, nil
}

func labelMap(expID, runID string) map[string]string {
	m := map[string]string{utils.LabelBench: "1", utils.LabelRun: runID}
	if expID != "" {
		m[utils.LabelExp] = expID
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

func workspaceVolume(runID string) string { return "bench-ws-" + runID }

func auxVolume(runID string) string { return "bench-aux-" + runID }

func agentContainer(runID string) string { return "bench-" + runID }

func helperName(runID, step string) string {
	return "bench-" + runID + "-" + step + "-" + strings.TrimPrefix(utils.NewID("h", 6), "h_")
}
