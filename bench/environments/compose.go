package environments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Multi-container tasks (X2, docs/multi-container.md): the settings a
// compose file gives a service (schemas.ComposeService) applied to its
// container, the run's named volumes, one-shot services, service logs, and
// the workspace service's environment for the agent.

const (
	// ServiceLogsFolder holds services/<id>.log in the run folder: each
	// compose service's output, captured after the agent exits.
	ServiceLogsFolder = "services"
	maxServiceLog     = 1 << 20
	runOnceTimeout    = 5 * time.Minute
	serviceMemory     = "4g"
	servicePids       = 1024
)

// composeVolume is the per-run docker volume of a compose named volume.
func composeVolume(runID, name string) string { return "bench-cv-" + runID + "-" + name }

// composeCreateArgs are the docker create flags a compose service adds
// before its image: its aliases, user, working folder, tmpfs mounts, volumes,
// CPU cap and entrypoint. A service without compose settings adds none. It
// is pure.
func composeCreateArgs(runID string, spec schemas.ServiceSpec) []string {
	x := spec.Compose
	if x == nil {
		return nil
	}
	var args []string
	for _, a := range x.Aliases {
		args = append(args, "--network-alias", a)
	}
	if x.User != "" {
		args = append(args, "--user", x.User)
	}
	if x.Workdir != "" {
		args = append(args, "--workdir", x.Workdir)
	}
	for _, t := range x.Tmpfs {
		args = append(args, "--tmpfs", t)
	}
	for _, v := range x.Volumes {
		if v.Name == "" && v.ReadOnly {
			// "-v" cannot say read-only for an anonymous volume: "/t:ro"
			// would read as a bind of /t. --mount expresses both.
			args = append(args, "--mount", "type=volume,target="+v.Target+",readonly")
			continue
		}
		m := v.Target
		if v.Name != "" {
			m = composeVolume(runID, v.Name) + ":" + v.Target
		}
		if v.ReadOnly {
			m += ":ro"
		}
		args = append(args, "-v", m)
	}
	if x.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(x.CPUs, 'f', -1, 64))
	}
	if x.EntrypointSet {
		first := ""
		if len(x.Entrypoint) > 0 {
			first = x.Entrypoint[0]
		}
		args = append(args, "--entrypoint", first)
	}
	return args
}

// serviceArgv is what follows the image: the rest of a compose entrypoint,
// then the command. It is pure.
func serviceArgv(spec schemas.ServiceSpec) []string {
	if x := spec.Compose; x != nil && x.EntrypointSet && len(x.Entrypoint) > 1 {
		return append(append([]string{}, x.Entrypoint[1:]...), spec.Argv...)
	}
	return spec.Argv
}

// serviceLimits are a service's process and memory caps: the bench's, or
// lower ones from its compose file. It is pure.
func serviceLimits(spec schemas.ServiceSpec) (string, string) {
	pids, mem := strconv.Itoa(servicePids), serviceMemory
	if x := spec.Compose; x != nil {
		if x.Pids > 0 && x.Pids < servicePids {
			pids = strconv.Itoa(x.Pids)
		}
		if x.MemoryBytes > 0 && x.MemoryBytes < 4<<30 {
			mem = strconv.FormatInt(x.MemoryBytes, 10)
		}
	}
	return pids, mem
}

// hiddenAliasArgs are a compose service's aliases for its connection to
// the hidden network. It is pure.
func hiddenAliasArgs(spec schemas.ServiceSpec) []string {
	if spec.Compose == nil {
		return nil
	}
	var args []string
	for _, a := range spec.Compose.Aliases {
		args = append(args, "--alias", a)
	}
	return args
}

// createComposeVolumes creates the run's compose named volumes, fresh and
// labelled with the run, so Destroy removes them with everything else.
func (e *dockerWorkspace) createComposeVolumes(ctx context.Context, expID, runID string, svcs []runService) error {
	seen := map[string]bool{}
	var names []string
	for _, s := range svcs {
		if s.spec.Compose == nil {
			continue
		}
		for _, v := range s.spec.Compose.Volumes {
			if v.Name != "" && !seen[v.Name] {
				seen[v.Name] = true
				names = append(names, v.Name)
			}
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if err := e.freshVolume(ctx, composeVolume(runID, n), expID, runID); err != nil {
			return err
		}
	}
	return nil
}

// runOnce reports whether a service is a compose one-shot service.
func runOnce(spec schemas.ServiceSpec) bool { return spec.Compose != nil && spec.Compose.RunOnce }

// waitDone waits for a one-shot service to exit and wants exit code 0.
func (e *dockerWorkspace) waitDone(ctx context.Context, name string) error {
	deadline := time.Now().Add(runOnceTimeout)
	for {
		running, code, err := e.containerState(ctx, name)
		if err != nil {
			return err
		}
		if !running {
			if code != 0 {
				return fmt.Errorf("one-shot service exited with code %d: %s", code, e.logTail(ctx, name))
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("one-shot service still running after %s: %s", runOnceTimeout, e.logTail(ctx, name))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPoll):
		}
	}
}

// captureServiceLogs writes each compose service's output (stdout and
// stderr, with docker's timestamps) to services/<id>.log in the run folder,
// capped at 1 MiB, where the seal covers it. A log that cannot be read is
// logged and left out: it is evidence, never a reason to fail the run.
func (e *dockerWorkspace) captureServiceLogs(ctx context.Context, runID, dir string, svcs []runService) {
	for _, s := range svcs {
		if s.spec.Compose == nil {
			continue
		}
		if err := e.captureServiceLog(ctx, runID, dir, s.spec.ID); err != nil {
			e.cfg.Logger.Warn("service log not captured", "run", runID, "service", s.spec.ID, "error", err.Error())
		}
	}
}

func (e *dockerWorkspace) captureServiceLog(ctx context.Context, runID, dir, id string) error {
	out := filepath.Join(dir, ServiceLogsFolder, id+".log")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	w := &cappedWriter{w: fh, limit: maxServiceLog}
	cctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, dockerBinary(e.cfg.Docker), "logs", "--timestamps", serviceContainer(runID, id))
	cmd.Stdout, cmd.Stderr = w, w
	runErr := cmd.Run()
	if err := errors.Join(w.err, fh.Close()); err != nil {
		return err
	}
	if runErr != nil {
		return fmt.Errorf("docker logs: %w", runErr)
	}
	if w.truncated {
		e.cfg.Logger.Debug("service log cut at its cap", "run", runID, "service", id, "cap", maxServiceLog)
	}
	return nil
}

// withTaskEnv returns plan with the task environment's variables added
// where the plan sets none: the workspace service's environment of a compose
// task reaches the agent, and a scripted control, as docker compose would
// give it to the service. The plan's own variables win. plan itself is not
// changed.
func withTaskEnv(rc *schemas.RunContext, plan *schemas.RunPlan) *schemas.RunPlan {
	if rc == nil || rc.Task == nil || rc.Task.Environment == nil || len(rc.Task.Environment.Env) == 0 || plan == nil {
		return plan
	}
	cp := *plan
	cp.Env = make(map[string]string, len(plan.Env)+len(rc.Task.Environment.Env))
	for k, v := range rc.Task.Environment.Env {
		cp.Env[k] = v
	}
	for k, v := range plan.Env {
		cp.Env[k] = v
	}
	return &cp
}
