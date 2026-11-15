package environments

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// SnapshotRefFile is the run file Snapshot writes: the image id of the frozen
// agent container, one line. The snapshot-mode graders read it, because a
// Grader sees only the run context and the diff.
const SnapshotRefFile = "snapshot.ref"

// Snapshot freezes the file system of the run's last agent container (it has
// exited; Exec keeps it in a snapshot-graded run) with docker commit, writes
// the image id to snapshot.ref in the run folder, removes the container and
// returns the id. The image is labelled with the run, so Destroy and the
// janitor remove it. Environment variables the plan gave the agent (its key,
// its wrapped files) are blanked in the image, PATH is reset to the image's
// own, so the verifier never sees them. Volumes (the workspace, memory) are
// not part of the image; the verifier mounts the workspace itself.
func (e *dockerWorkspace) Snapshot(ctx context.Context, rc *schemas.RunContext) (string, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return "", err
	}
	if rc.Dir == "" {
		return "", fmt.Errorf("snapshot run %s: no run folder", runID)
	}
	name, err := e.lastAgent(ctx, runID)
	if err != nil {
		return "", fmt.Errorf("snapshot run %s: %w", runID, err)
	}
	changes, err := e.snapshotChanges(ctx, name)
	if err != nil {
		return "", fmt.Errorf("snapshot run %s: %w", runID, err)
	}
	args := []string{"commit"}
	for _, c := range changes {
		args = append(args, "--change", c)
	}
	args = append(args, "--change", "LABEL "+utils.LabelBench+"=1", "--change", "LABEL "+utils.LabelRun+"="+runID)
	if expID != "" {
		args = append(args, "--change", "LABEL "+utils.LabelExp+"="+expID)
	}
	res, err := e.cfg.Docker.MustRun(ctx, append(args, name, snapshotTag(runID))...)
	if err != nil {
		return "", fmt.Errorf("snapshot run %s: %w", runID, err)
	}
	id := strings.TrimSpace(string(res.Stdout))
	if !strings.HasPrefix(id, "sha256:") {
		return "", fmt.Errorf("snapshot run %s: docker commit printed %q, not an image id", runID, truncate(id, 100))
	}
	if err := os.WriteFile(filepath.Join(rc.Dir, SnapshotRefFile), []byte(id+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("snapshot run %s: write %s: %w", runID, SnapshotRefFile, err)
	}
	e.removeContainers(ctx, name)
	e.cfg.Logger.Debug("agent container frozen", "run", runID, "container", name, "image", id)
	return id, nil
}

// snapshotMode reports whether the run is graded from a snapshot.
func snapshotMode(rc *schemas.RunContext) bool {
	return utils.VerifierMode(rc.Suite) == schemas.VerifierSnapshot
}

// lastAgent finds the run's most recent agent container: the highest session
// number, or the single-session container.
func (e *dockerWorkspace) lastAgent(ctx context.Context, runID string) (string, error) {
	res, err := e.cfg.Docker.MustRun(ctx, "ps", "-a", "--filter", "label="+utils.LabelRun+"="+runID, "--format", "{{.Names}}")
	if err != nil {
		return "", err
	}
	name := pickAgent(runID, strings.Fields(string(res.Stdout)))
	if name == "" {
		return "", fmt.Errorf("the agent container of run %s is gone; snapshot grading needs it kept after the agent exits", runID)
	}
	return name, nil
}

// pickAgent returns the agent container with the highest session number
// among names, or "". It is pure.
func pickAgent(runID string, names []string) string {
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(agentContainer(runID)) + `(-s([0-9]+))?$`)
	best, bestN := "", -1
	for _, n := range names {
		m := re.FindStringSubmatch(n)
		if m == nil {
			continue
		}
		k := 0
		if m[2] != "" {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				continue
			}
			k = n
		}
		if k > bestN {
			best, bestN = n, k
		}
	}
	return best
}

// snapshotChanges returns the docker commit --change lines that blank every
// environment variable the container has beyond its image's, and put PATH
// back to the image's own.
func (e *dockerWorkspace) snapshotChanges(ctx context.Context, name string) ([]string, error) {
	res, err := e.cfg.Docker.MustRun(ctx, "inspect", "--format", "{{json .Config.Env}}\n{{.Image}}", name)
	if err != nil {
		return nil, fmt.Errorf("inspect agent container: %w", err)
	}
	cenv, image, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
	ires, err := e.cfg.Docker.MustRun(ctx, "image", "inspect", "--format", "{{json .Config.Env}}", strings.TrimSpace(image))
	if err != nil {
		return nil, fmt.Errorf("inspect agent image: %w", err)
	}
	var cvars, ivars []string
	if err := json.Unmarshal([]byte(cenv), &cvars); err != nil {
		return nil, fmt.Errorf("parse container env: %w", err)
	}
	if err := json.Unmarshal(ires.Stdout, &ivars); err != nil {
		return nil, fmt.Errorf("parse image env: %w", err)
	}
	return envChanges(cvars, ivars), nil
}

// envChanges is the pure part of snapshotChanges.
func envChanges(container, image []string) []string {
	own := map[string]string{}
	for _, kv := range image {
		k, v, _ := strings.Cut(kv, "=")
		own[k] = v
	}
	var out []string
	for _, kv := range container {
		k, v, _ := strings.Cut(kv, "=")
		if iv, ok := own[k]; ok && iv == v {
			continue
		}
		switch {
		case !envKeyRe.MatchString(k):
		case k == "PATH" && own["PATH"] != "":
			out = append(out, "ENV PATH="+strconv.Quote(own["PATH"]))
		case k == "PATH":
			out = append(out, "ENV PATH="+defaultPath)
		case own[k] != "":
			out = append(out, "ENV "+k+"="+strconv.Quote(own[k]))
		default:
			out = append(out, "ENV "+k+"=")
		}
	}
	return out
}

// snapshotTag is the tag of a run's snapshot image.
func snapshotTag(runID string) string {
	return "bench/snap-" + utils.ImageName(runID) + ":latest"
}
