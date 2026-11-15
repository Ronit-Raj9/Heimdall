package environments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// reservedAgentPaths are container paths a memory path or a plan folder may
// not be at or under: the bench's own mounts and the kernel's.
var reservedAgentPaths = []string{defaultWorkdir, auxMount, utils.DefaultMemoryDir, benchMount, "/proc", "/sys", "/dev"}

// BeginSession prepares the workspace for session index (0-based) of a
// multi-session task. A session after the first that does not keep the
// workspace gets it reset to the task's starting state (the same seed as
// Create). The session's files folder is then copied in, and the baseline is
// recorded again, so the exported diff is against the start of the last
// session. A kept workspace loses its .git history to the new baseline: git
// never runs on an agent-written .git folder. The memory volume and the
// agent's memory paths are not touched. rc.Handle.BaselineRef is updated in
// place. Session 0 without files changes nothing.
func (e *dockerWorkspace) BeginSession(ctx context.Context, rc *schemas.RunContext, index int) error {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return err
	}
	if rc.Handle == nil || rc.Task == nil {
		return fmt.Errorf("begin session for run %s: needs a workspace handle and a task", runID)
	}
	if index < 0 || index >= len(rc.Task.Sessions) {
		return fmt.Errorf("begin session for run %s: task %s has no session %d", runID, rc.Task.ID, index)
	}
	s := rc.Task.Sessions[index]
	files := ""
	if s.Files != "" {
		if files, err = utils.ResolveInside(rc.Task.Dir, s.Files); err != nil {
			return fmt.Errorf("begin session %s for run %s: files: %w", s.ID, runID, err)
		}
		if st, err := os.Stat(files); err != nil || !st.IsDir() {
			return fmt.Errorf("begin session %s for run %s: files %s is not a folder", s.ID, runID, files)
		}
	}
	reset := index > 0 && !s.KeepWorkspace
	if !reset && files == "" && index == 0 {
		return nil
	}
	var ref string
	if reset {
		ref, err = e.seed(ctx, rc, rc.Handle, expID, runID, seedOptions{clear: true, session: files})
	} else {
		ref, err = e.rebaseline(ctx, rc, expID, runID, files)
	}
	if err != nil {
		return fmt.Errorf("begin session %s for run %s: %w", s.ID, runID, err)
	}
	rc.Handle.BaselineRef = ref
	e.cfg.Logger.Debug("session begun", "run", runID, "session", s.ID, "reset", reset, "baseline", ref)
	return nil
}

// rebaselineScript copies the session files over a kept workspace and
// replaces its .git with a fresh baseline commit of what is there now.
const rebaselineScript = `set -eu
cd /workspace
if [ -n "${BENCH_SESSION:-}" ]; then cp -dR --preserve=mode,timestamps /bench-session/. /workspace/; fi
rm -rf /workspace/.git
# git as root refuses a work tree it does not own; uid 1000 gets it back below.
chown 0:0 /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp GIT_TERMINAL_PROMPT=0
export GIT_AUTHOR_DATE="$BENCH_DATE" GIT_COMMITTER_DATE="$BENCH_DATE"
g() { git -c core.hooksPath=/dev/null -c core.fsmonitor=false -c core.autocrlf=false "$@"; }
g -c init.defaultBranch=main init -q
g add -A --force
g -c user.name=bench -c user.email=bench@local -c commit.gpgsign=false commit -q --no-verify --allow-empty -m baseline
ref="$(git rev-parse HEAD)"
chown -R 1000:1000 /workspace
echo "$ref"
`

// rebaseline runs rebaselineScript and returns the new baseline commit id.
// The aux volume is not mounted: the agent could have pointed a symlink at it.
func (e *dockerWorkspace) rebaseline(ctx context.Context, rc *schemas.RunContext, expID, runID, files string) (string, error) {
	args := e.helperArgs(helperName(runID, "rebase"), expID, runID, []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"})
	args = append(args, "-v", rc.Handle.Volume+":/workspace", "-e", "BENCH_DATE="+baselineDate)
	if files != "" {
		args = append(args, "-v", files+":/bench-session:ro", "-e", "BENCH_SESSION=1")
	}
	args = append(args, e.helperImage(rc), "-c", rebaselineScript)
	res, err := e.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("rebaseline workspace: %w", err)
	}
	return lastRef(res.Stdout)
}

// memoryEnabled reports whether the run gets the /memory volume and the
// agent's memory path volumes: a multi-session task, or an agent that
// declares memory. Other runs keep the exact container they always had.
func memoryEnabled(rc *schemas.RunContext) bool {
	return (rc.Task != nil && len(rc.Task.Sessions) > 0) || (rc.Agent != nil && rc.Agent.Memory != nil)
}

// memoryPaths returns the agent's memory paths, validated. It is pure.
func memoryPaths(rc *schemas.RunContext) ([]string, error) {
	if rc.Agent == nil || rc.Agent.Memory == nil {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, p := range rc.Agent.Memory.Paths {
		if err := checkAgentPath(p); err != nil {
			return nil, fmt.Errorf("memory path: %w", err)
		}
		if seen[p] {
			return nil, fmt.Errorf("memory path %s is listed twice", p)
		}
		seen[p] = true
	}
	return rc.Agent.Memory.Paths, nil
}

// checkAgentPath refuses a container path that is not absolute and clean, or
// that is at or under a reserved path. It is pure.
func checkAgentPath(p string) error {
	if !path.IsAbs(p) || path.Clean(p) != p || p == "/" {
		return fmt.Errorf("%q must be an absolute clean container path", p)
	}
	for _, r := range reservedAgentPaths {
		if p == r || strings.HasPrefix(p, r+"/") {
			return fmt.Errorf("%q is inside %s, which the bench reserves", p, r)
		}
	}
	return nil
}

// createMemory makes the run's memory volumes fresh and empty: /memory and
// one per memory path. Create calls it, so session 1 starts with nothing.
func (e *dockerWorkspace) createMemory(ctx context.Context, rc *schemas.RunContext, expID, runID string) error {
	if !memoryEnabled(rc) {
		return nil
	}
	paths, err := memoryPaths(rc)
	if err != nil {
		return err
	}
	for _, v := range memoryVolumes(runID, len(paths)) {
		if err := e.freshVolume(ctx, v, expID, runID); err != nil {
			return err
		}
	}
	return nil
}

// agentMounts prepares and returns the extra mounts of an agent container:
// the workspace again at the task image's workspace path, the memory volumes
// (handed to the plan's user), and one read-only volume per plan folder,
// filled from the host folder before the agent starts. A memory path or plan
// folder under the home tmpfs or /tmp is not mounted there: docker would
// create its parent folders owned by root inside the agent's home, and the
// agent could no longer write its own config next to them. Its volume is
// mounted under /bench instead and returned as a link the launch wrapper
// makes, as the agent user, before the agent starts.
func (e *dockerWorkspace) agentMounts(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan, expID, runID string) ([]string, []link, error) {
	var mounts []string
	var links []link
	if wp := workspacePath(rc); wp != "" && wp != defaultWorkdir {
		mounts = append(mounts, "-v", rc.Handle.Volume+":"+wp)
	}
	var own []string
	if memoryEnabled(rc) {
		paths, err := memoryPaths(rc)
		if err != nil {
			return nil, nil, err
		}
		vols := memoryVolumes(runID, len(paths))
		mounts = append(mounts, "-v", vols[0]+":"+utils.DefaultMemoryDir)
		for i, p := range paths {
			at := p
			if linked(p) {
				at = benchMount + "/memory/" + strconv.Itoa(i)
				links = append(links, link{dst: p, src: at})
			}
			mounts = append(mounts, "-v", vols[i+1]+":"+at)
		}
		own = vols
	}
	dirs, err := planDirs(plan)
	if err != nil {
		return nil, nil, err
	}
	for i, d := range dirs {
		v := dirVolume(runID, i)
		if err := e.freshVolume(ctx, v, expID, runID); err != nil {
			return nil, nil, err
		}
		at := d.dst
		if linked(d.dst) {
			at = benchMount + "/dirs/" + strconv.Itoa(i)
			links = append(links, link{dst: d.dst, src: at})
		}
		mounts = append(mounts, "-v", v+":"+at+":ro")
	}
	if err := e.fillAgentVolumes(ctx, rc, plan, expID, runID, own, dirs); err != nil {
		return nil, nil, err
	}
	return mounts, links, nil
}

// benchMount is where volumes that reach the agent through a link are
// mounted.
const benchMount = "/bench"

// linked reports whether a container path is reached through a link rather
// than a mount at the path itself: it lies under the agent's home tmpfs or
// /tmp, folders the agent user owns. It is pure.
func linked(p string) bool {
	return strings.HasPrefix(p, agentHome+"/") || strings.HasPrefix(p, "/tmp/")
}

// planDir is one RunPlan.Dirs entry with its host folder resolved.
type planDir struct {
	dst string // container path
	src string // real host folder
}

// planDirs validates and resolves the plan's folders, in container path
// order. The host path is resolved right before the bind mount, so a folder
// swapped for a link is mounted where it really is.
func planDirs(plan *schemas.RunPlan) ([]planDir, error) {
	var out []planDir
	for _, dst := range sortedKeys(plan.Dirs) {
		if err := checkAgentPath(dst); err != nil {
			return nil, fmt.Errorf("plan folder: %w", err)
		}
		src := plan.Dirs[dst]
		if !filepath.IsAbs(src) {
			return nil, fmt.Errorf("plan folder %s: host path %q is not absolute", dst, src)
		}
		real, err := filepath.EvalSymlinks(src)
		if err != nil {
			return nil, fmt.Errorf("plan folder %s: %w", dst, err)
		}
		if st, err := os.Stat(real); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("plan folder %s: %s is not a folder", dst, src)
		}
		out = append(out, planDir{dst: dst, src: real})
	}
	return out, nil
}

// fillScript hands the memory volumes to the agent user and fills each plan
// folder volume, owned by root and readable by all.
const fillScript = `set -eu
for m in /bench-mem/*; do
	[ -d "$m" ] || continue
	if [ -n "$BENCH_UID" ]; then chown "$BENCH_UID:$BENCH_GID" "$m"; chmod 0700 "$m"; else chmod 1777 "$m"; fi
done
for d in /bench-dir/*; do
	[ -d "$d" ] || continue
	cp -dR --preserve=mode,timestamps "/bench-src/$(basename "$d")/." "$d/"
	chown -R 0:0 "$d"
	chmod -R a+rX,go-w "$d"
done
`

// fillAgentVolumes runs fillScript once for every memory volume and plan
// folder of one Exec. It is skipped when there are none.
func (e *dockerWorkspace) fillAgentVolumes(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan, expID, runID string, mem []string, dirs []planDir) error {
	if len(mem) == 0 && len(dirs) == 0 {
		return nil
	}
	user := plan.User
	if user == "" {
		user = defaultUser
	}
	uid, gid, _ := numericOwner(user)
	args := e.helperArgs(helperName(runID, "fill"), expID, runID, []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"})
	for i, v := range mem {
		args = append(args, "-v", v+":/bench-mem/"+strconv.Itoa(i))
	}
	for i, d := range dirs {
		n := strconv.Itoa(i)
		args = append(args, "-v", dirVolume(runID, i)+":/bench-dir/"+n, "-v", d.src+":/bench-src/"+n+":ro")
	}
	args = append(args, "-e", "BENCH_UID="+uid, "-e", "BENCH_GID="+gid, e.helperImage(rc), "-c", fillScript)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("prepare agent volumes: %w", err)
	}
	return nil
}

// streamPaths are where one Exec writes the agent's stdout and stderr. A
// session writes agent.stream.<n>.jsonl and agent.stderr.<n>.txt (n 1-based)
// and then appends them to agent.stream.jsonl and agent.stderr.txt, which the
// first session starts empty.
func streamPaths(rc *schemas.RunContext) (stdout, stderr string, session int) {
	if rc.Session == nil {
		return filepath.Join(rc.Dir, "agent.stream.jsonl"), filepath.Join(rc.Dir, "agent.stderr.txt"), 0
	}
	n := rc.SessionIndex + 1
	return filepath.Join(rc.Dir, fmt.Sprintf("agent.stream.%d.jsonl", n)), filepath.Join(rc.Dir, fmt.Sprintf("agent.stderr.%d.txt", n)), n
}

// appendSession appends one session's stream files to the concatenated ones.
func appendSession(dir string, n int, stdout, stderr string) error {
	for _, p := range [][2]string{{stdout, "agent.stream.jsonl"}, {stderr, "agent.stderr.txt"}} {
		flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
		if n == 1 {
			flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		}
		if err := appendFile(filepath.Join(dir, p[1]), p[0], flags); err != nil {
			return err
		}
	}
	return nil
}

func appendFile(dst, src string, flags int) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", dst, err)
	}
	_, cerr := io.Copy(out, in)
	if err := errors.Join(cerr, out.Close()); err != nil {
		return fmt.Errorf("append %s: %w", dst, err)
	}
	return nil
}

// agentName is the agent container of the current Exec: bench-<run> for a
// single-session run, bench-<run>-s<n> for session n (1-based).
func agentName(rc *schemas.RunContext, runID string) string {
	if rc.Session == nil {
		return agentContainer(runID)
	}
	return agentContainer(runID) + "-s" + strconv.Itoa(rc.SessionIndex+1)
}

// staleAgents are the agent containers that must be gone before this Exec
// starts: its own name and every earlier session's.
func staleAgents(rc *schemas.RunContext, runID string) []string {
	names := []string{agentContainer(runID)}
	if rc.Session != nil {
		for i := 1; i <= rc.SessionIndex+1; i++ {
			names = append(names, agentContainer(runID)+"-s"+strconv.Itoa(i))
		}
	}
	return names
}

// memoryVolumes are /memory's volume then one per memory path.
func memoryVolumes(runID string, paths int) []string {
	out := []string{"bench-mem-" + runID}
	for i := 0; i < paths; i++ {
		out = append(out, "bench-memp-"+runID+"-"+strconv.Itoa(i))
	}
	return out
}

func dirVolume(runID string, i int) string { return "bench-dir-" + runID + "-" + strconv.Itoa(i) }

// lastRef reads the baseline commit id a helper printed last.
func lastRef(out []byte) (string, error) {
	lines := strings.Fields(string(out))
	if len(lines) == 0 || !refRe.MatchString(lines[len(lines)-1]) {
		return "", fmt.Errorf("no baseline commit id in helper output %q", truncate(string(out), 200))
	}
	return lines[len(lines)-1], nil
}
