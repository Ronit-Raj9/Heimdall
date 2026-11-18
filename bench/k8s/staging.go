package k8s

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Staging volumes for the workspace probe layers (docs/kubernetes.md,
// "Probes"). The fixture and tool shim layers arm between Create and Exec by
// writing into the run's workspace and aux volumes through Docker helpers on
// the controller (probes/fixture.go, probes/toolshim.go). For a run with
// such a probe the driver gives the handle two local Docker volumes the
// layers write into unchanged; Exec lays their files into the pod after the
// baseline commit (as under Docker, where they land after Create's commit),
// and copies the pod's aux log/ folder back after the agent, so the shim
// log evidence and the layer's receipts read what the shims wrote.

const (
	stagedMax = 64 << 20
	// stageTar prints a tar of the staged volume's top-level entries (never
	// "." itself, so the pod folder keeps its own owner and mode), nothing
	// when it is empty.
	stageTar = `cd /s && set -- $(ls -A) && [ "$#" -gt 0 ] && exec tar -cf - -- "$@"; exit 0`
	// copyLogScript prints a tar of the pod's aux log/ folder, nothing when
	// the shims wrote none.
	copyLogScript = `cd /bench-aux && [ -d log ] && exec tar -cf - log; exit 0`
)

// stagedLayer reports whether the run's probe writes into the workspace or
// the aux volume before the agent starts.
func stagedLayer(rc *schemas.RunContext) bool {
	return rc != nil && rc.Probe != nil && (rc.Probe.Layer == schemas.LayerFixture || rc.Probe.Layer == schemas.LayerToolShim)
}

func stagingVolume(runID, part string) string { return "bench-k8s-" + part + "-" + runID }

func dockerBin(d utils.Docker) string {
	if d.Binary != "" {
		return d.Binary
	}
	return "docker"
}

// createStaging makes the run's two staging volumes, fresh and labelled
// with the run, so the Docker janitor finds a leftover.
func (d *Driver) createStaging(ctx context.Context, r runRef, h *schemas.EnvironmentHandle) error {
	h.Volume, h.AuxVolume = stagingVolume(r.runID, "ws"), stagingVolume(r.runID, "aux")
	for _, v := range []string{h.Volume, h.AuxVolume} {
		if _, err := d.cfg.Docker.MustRun(ctx, "volume", "rm", "-f", v); err != nil {
			return fmt.Errorf("remove stale staging volume %s: %w", v, err)
		}
		args := append([]string{"volume", "create"}, utils.LabelArgs(r.expID, r.runID)...)
		if _, err := d.cfg.Docker.MustRun(ctx, append(args, v)...); err != nil {
			return fmt.Errorf("create staging volume %s: %w", v, err)
		}
	}
	return nil
}

// readStaged returns a tar of a staging volume's files, read-only in a
// helper with no network.
func (d *Driver) readStaged(ctx context.Context, r runRef, image, vol string) ([]byte, error) {
	args := []string{"run", "--rm", "--network", "none", "--user", "0", "--cap-drop", "ALL", "--cap-add", "DAC_OVERRIDE",
		"--security-opt", "no-new-privileges", "--log-driver", "none"}
	args = append(args, utils.LabelArgs(r.expID, r.runID)...)
	args = append(args, "-v", vol+":/s:ro", "--entrypoint", "/bin/sh", image, "-c", stageTar)
	res, err := d.cfg.Docker.MustRun(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("read staging volume %s: %w", vol, err)
	}
	if len(res.Stdout) > stagedMax {
		return nil, fmt.Errorf("staging volume %s holds over %d bytes", vol, stagedMax)
	}
	return res.Stdout, nil
}

// layStaged extracts the staging volumes into the pod's workspace and aux
// folders through the prep container, keeping the files' owners and modes
// (the shim folder stays root-owned).
func (d *Driver) layStaged(ctx context.Context, rc *schemas.RunContext, r runRef, image string) error {
	if rc.Handle == nil || rc.Handle.Volume == "" {
		return nil
	}
	for _, x := range []struct{ vol, dst string }{{rc.Handle.Volume, "/workspace"}, {rc.Handle.AuxVolume, "/opt/bench"}} {
		tarball, err := d.readStaged(ctx, r, image, x.vol)
		if err != nil {
			return err
		}
		if len(tarball) == 0 {
			continue
		}
		if _, err := d.cfg.Client.ExecOutput(ctx, r.ns, podName, ctrPrep, []string{"tar", "-x", "-f", "-", "-C", x.dst}, bytes.NewReader(tarball), 1<<16); err != nil {
			return fmt.Errorf("lay staged files into %s: %w", x.dst, err)
		}
	}
	return nil
}

// copyBackLog copies the pod's aux log/ folder into the staging aux volume
// after the agent, where the shim log source and the layer's receipts read
// it. Problems are logged: a missing log reads as a shim that never ran.
func (d *Driver) copyBackLog(ctx context.Context, rc *schemas.RunContext, r runRef, image string) {
	if rc.Handle == nil || rc.Handle.AuxVolume == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	tarball, err := d.cfg.Client.ExecOutput(cctx, r.ns, podName, ctrBench, []string{"/bin/sh", "-c", copyLogScript}, nil, stagedMax)
	if err != nil || len(tarball) == 0 {
		if err != nil {
			d.cfg.Logger.Warn("copy the shim log out of the pod", "run", r.runID, "error", err.Error())
		}
		return
	}
	args := []string{"run", "-i", "--rm", "--network", "none", "--user", "0", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE",
		"--cap-add", "FOWNER", "--security-opt", "no-new-privileges", "--log-driver", "none"}
	args = append(args, utils.LabelArgs(r.expID, r.runID)...)
	args = append(args, "-v", rc.Handle.AuxVolume+":/a", "--entrypoint", "tar", image, "-x", "-f", "-", "-C", "/a")
	cmd := exec.CommandContext(cctx, dockerBin(d.cfg.Docker), args...)
	cmd.Stdin = bytes.NewReader(tarball)
	if out, err := cmd.CombinedOutput(); err != nil {
		d.cfg.Logger.Warn("copy the shim log into the staging volume", "run", r.runID, "error", strings.TrimSpace(string(out)))
	}
}

// removeStaging removes the run's staging volumes (and any container left
// on them), idempotently.
func (d *Driver) removeStaging(ctx context.Context, r runRef) error {
	filter := "label=" + utils.LabelRun + "=" + r.runID
	if ps, err := d.cfg.Docker.MustRun(ctx, "ps", "-aq", "--filter", filter); err == nil {
		if ids := strings.Fields(string(ps.Stdout)); len(ids) > 0 {
			d.cfg.Docker.Run(ctx, append([]string{"rm", "-f"}, ids...)...)
		}
	}
	_, err := d.cfg.Docker.MustRun(ctx, "volume", "rm", "-f", stagingVolume(r.runID, "ws"), stagingVolume(r.runID, "aux"))
	return err
}
