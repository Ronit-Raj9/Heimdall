package environments

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// EnsureTaskImage makes the task's own image (Task.Environment) present on
// the local docker, building its Dockerfile or pulling its tag when missing,
// and returns the tag and the image id. A task without its own image returns
// empty strings. The planner calls it before it records the manifest, and
// admission before it grades controls; Create calls it again, so a run never
// starts from a missing image. Build output goes to out (nil discards it).
func EnsureTaskImage(ctx context.Context, d utils.Docker, s *schemas.Suite, t *schemas.Task, out io.Writer) (string, string, error) {
	suiteID := ""
	if s != nil {
		suiteID = s.ID
	}
	img, ok, err := utils.TaskImage(suiteID, t)
	if err != nil || !ok {
		return "", "", err
	}
	img.Pinned = utils.SuiteLockedPin(s, t.ID, "task", img.Tag) // library registry: pulled by the lock's digest (bench/imagepin)
	id, err := d.EnsureImage(ctx, img, out)
	if err != nil {
		return "", "", fmt.Errorf("task %s image %s: %w", t.ID, img.Tag, err)
	}
	return img.Tag, id, nil
}

// EnsureTaskImage implements engine.TaskImageEnsurer with this environment's
// docker.
func (e *dockerWorkspace) EnsureTaskImage(ctx context.Context, s *schemas.Suite, t *schemas.Task) (string, string, error) {
	return EnsureTaskImage(ctx, e.cfg.Docker, s, t, nil)
}

// seedOptions changes how seed fills the workspace volume.
type seedOptions struct {
	clear   bool   // empty the volume first (a session reset)
	session string // host folder copied over the seeded files
}

// populateScript runs as root in the trusted helper. Nothing in the volume is
// agent-written yet (a reset deletes it first, without following links), so
// running git here is safe; hooks are still disabled. The source is the
// task repo bind-mounted at /bench-src, or a tar of the task image's
// workspace folder on stdin.
const populateScript = `set -eu
if [ -n "${BENCH_CLEAR:-}" ]; then find /workspace -mindepth 1 -delete; fi
if [ -n "$(ls -A /workspace)" ]; then echo "workspace volume is not empty" >&2; exit 3; fi
# A new volume inherits the image's /workspace owner; git as root refuses a
# repo it does not own, so hand everything to uid 1000 only after the commit.
chown 0:0 /workspace
if [ -n "${BENCH_SRC_TAR:-}" ]; then
	tar -x -f - -C /workspace --no-same-owner
	chown 0:0 /workspace
else
	cp -dR --preserve=mode,timestamps /bench-src/. /workspace/
fi
if [ -n "${BENCH_NOTES:-}" ]; then
	mkdir -p "$(dirname "/workspace/$BENCH_NOTES")"
	cp /bench-in/notes "/workspace/$BENCH_NOTES"
fi
if [ -n "${BENCH_SESSION:-}" ]; then cp -dR --preserve=mode,timestamps /bench-session/. /workspace/; fi
mkdir -p /opt/bench/shim /opt/bench/log
cd /workspace
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/tmp
export GIT_AUTHOR_DATE="$BENCH_DATE" GIT_COMMITTER_DATE="$BENCH_DATE"
git -c init.defaultBranch=main init -q
git -c core.hooksPath=/dev/null add -A --force
git -c core.hooksPath=/dev/null -c user.name=bench -c user.email=bench@local -c commit.gpgsign=false commit -q --no-verify --allow-empty -m baseline
ref="$(git rev-parse HEAD)"
chown -R 1000:1000 /workspace /opt/bench
# Agent events area: the events file folder, root-owned and sticky so any
# agent user can append and none can remove another's file. It is made here,
# by the helper that already holds DAC_OVERRIDE, because /opt/bench now
# belongs to uid 1000 and a later root helper without it could not mkdir.
if [ -n "${BENCH_EVENTS:-}" ]; then
	mkdir -p /opt/bench/events
	chown 0:0 /opt/bench/events
	chmod 1777 /opt/bench/events
fi
echo "$ref"
`

// seed fills the workspace volume with the task's starting state and returns
// the baseline commit id: from the task repo folder, or, when the task's
// environment names a workspace path, from that folder of the task image. The
// clean notes file and the session's files are laid on top.
func (e *dockerWorkspace) seed(ctx context.Context, rc *schemas.RunContext, h *schemas.EnvironmentHandle, expID, runID string, o seedOptions) (string, error) {
	notesRel, notesHost, err := cleanNotes(rc)
	if err != nil {
		return "", err
	}
	args := e.helperArgs(helperName(runID, "prep"), expID, runID, []string{"CHOWN", "DAC_OVERRIDE", "DAC_READ_SEARCH", "FOWNER"})
	args = append(args,
		"-v", h.Volume+":/workspace",
		"-v", h.AuxVolume+":"+auxMount,
		"-e", "BENCH_DATE="+baselineDate)
	if o.clear {
		args = append(args, "-e", "BENCH_CLEAR=1")
	}
	if e.cfg.EventsCollector {
		args = append(args, "-e", "BENCH_EVENTS=1") // Agent events area: the events folder (populateScript)
	}
	if notesRel != "" {
		args = append(args, "-v", notesHost+":/bench-in/notes:ro", "-e", "BENCH_NOTES="+notesRel)
	}
	if o.session != "" {
		args = append(args, "-v", o.session+":/bench-session:ro", "-e", "BENCH_SESSION=1")
	}
	wp := workspacePath(rc)
	if wp == "" {
		repoDir, err := taskRepo(rc)
		if err != nil {
			return "", err
		}
		args = append(args, "-v", repoDir+":/bench-src:ro", e.helperImage(rc), "-c", populateScript)
		res, err := e.cfg.Docker.MustRun(ctx, args...)
		if err != nil {
			return "", fmt.Errorf("populate workspace: %w", err)
		}
		return lastRef(res.Stdout)
	}
	tag, _, err := e.EnsureTaskImage(ctx, rc.Suite, rc.Task)
	if err != nil {
		return "", err
	}
	args = append(args, "-e", "BENCH_SRC_TAR=1", e.helperImage(rc), "-c", populateScript)
	out, err := e.fromImage(ctx, expID, runID, tag, wp, append([]string{"run", "-i"}, args[1:]...))
	if err != nil {
		return "", fmt.Errorf("populate workspace from %s:%s: %w", tag, wp, err)
	}
	return lastRef(out)
}

// fromImage streams folder wp of image into the helper command `to` (which
// reads a tar on stdin) and returns the helper's stdout. The image is only
// created, never started, so no task image code runs.
func (e *dockerWorkspace) fromImage(ctx context.Context, expID, runID, image, wp string, to []string) ([]byte, error) {
	src := helperName(runID, "src")
	create := append([]string{"create", "--name", src}, utils.LabelArgs(expID, runID)...)
	create = append(create, "--network", "none", "--entrypoint", "/bench-none", image)
	if _, err := e.cfg.Docker.MustRun(ctx, create...); err != nil {
		return nil, fmt.Errorf("create source container: %w", err)
	}
	defer e.removeContainers(ctx, src)
	return e.cfg.Docker.Pipe(ctx, []string{"cp", src + ":" + strings.TrimSuffix(wp, "/") + "/.", "-"}, to)
}

// workspacePath is the folder of the task image that seeds the workspace, or
// "" when the workspace comes from the task repo.
func workspacePath(rc *schemas.RunContext) string {
	if rc == nil || rc.Task == nil || rc.Task.Environment == nil {
		return ""
	}
	return rc.Task.Environment.WorkspacePath
}

// checkWorkspacePath refuses a workspace path that is not a clean absolute
// folder the agent container can mount the workspace on. It is pure.
func checkWorkspacePath(wp string) error {
	if wp == "" {
		return nil
	}
	if !path.IsAbs(wp) || path.Clean(wp) != wp || wp == "/" {
		return fmt.Errorf("workspace_path %q must be a clean absolute folder", wp)
	}
	for _, r := range []string{auxMount, utils.DefaultMemoryDir, "/proc", "/sys", "/dev", "/tmp", agentHome} {
		if wp == r || strings.HasPrefix(wp, r+"/") {
			return fmt.Errorf("workspace_path %q is inside %s, which the bench reserves", wp, r)
		}
	}
	return nil
}

// taskRepo is the real path of the task's repo folder.
func taskRepo(rc *schemas.RunContext) (string, error) {
	// Resolved right before the bind mount: docker follows symlinks, so a
	// repo folder swapped for a link after the suite loaded must not mount
	// a host folder outside the task.
	repoDir, err := utils.ResolveInside(rc.Task.Dir, rc.Task.RepoDir)
	if err != nil {
		return "", fmt.Errorf("task repo: %w", err)
	}
	if st, err := os.Stat(repoDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("task repo %s is not a folder", repoDir)
	}
	return repoDir, nil
}

// hashScript prints "<sha256>  ./<path>" for every regular file of the
// workspace outside .git.
const hashScript = `cd /workspace && find . -path ./.git -prune -o -type f -print0 | xargs -0 -r sha256sum`

// volumeProtected hashes the protected files of an image-seeded workspace,
// read in a helper, since there is no host repo folder to hash.
func (e *dockerWorkspace) volumeProtected(ctx context.Context, rc *schemas.RunContext, h *schemas.EnvironmentHandle, expID, runID string) (map[string]string, error) {
	out := map[string]string{}
	if len(rc.Task.Protected) == 0 {
		return out, nil
	}
	args := e.helperArgs(helperName(runID, "hash"), expID, runID, []string{"DAC_OVERRIDE", "DAC_READ_SEARCH"})
	args = append(args, "-v", h.Volume+":/workspace:ro", e.helperImage(rc), "-c", hashScript)
	res, err := e.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("hash protected files: %w", err)
	}
	for p, sum := range parseSHA256Sum(res.Stdout) {
		for _, g := range rc.Task.Protected {
			if matchGlob(g, p) {
				out[p] = sum
				break
			}
		}
	}
	return out, nil
}

// parseSHA256Sum reads sha256sum output for paths under "./". A line for an
// escaped name (sha256sum starts it with a backslash) is skipped. It is pure.
func parseSHA256Sum(raw []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		sum, name, ok := strings.Cut(sc.Text(), "  ")
		if !ok || len(sum) != 64 || !strings.HasPrefix(name, "./") {
			continue
		}
		out[strings.TrimPrefix(name, "./")] = sum
	}
	return out
}
