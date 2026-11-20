package environments

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Team runs (docs/multi-agent.md): one container per team member, all on the
// run's networks with the shared workspace volume, each with its own plan
// and gateway key. The members start together and the run ends when the last
// one exits or the wall budget passes.

var _ schemas.TeamEnvironment = (*dockerWorkspace)(nil)

// teamObserverReason is why a team run has no step observers yet: the
// process observer joins one container's PID namespace, and the workspace
// observer cannot tell which member wrote a file.
const teamObserverReason = "team run: the step observers do not run per member yet"

// ExecTeam implements schemas.TeamEnvironment. Each member container is
// created and joined to the service network first, then all are started at
// once and attached in parallel. A member that cannot be created ends the
// run before any member starts; a member that fails later has its error in
// its result. Every member's output goes to agent.<id>.stream.jsonl and
// agent.<id>.stderr.txt, and is appended, member by member, to
// agent.stream.jsonl and agent.stderr.txt for the run's parsers.
func (e *dockerWorkspace) ExecTeam(ctx context.Context, rc *schemas.RunContext, members []schemas.MemberRun) ([]schemas.MemberResult, error) {
	expID, runID, err := runIDs(rc)
	if err != nil {
		return nil, err
	}
	if rc.Handle == nil || rc.Dir == "" || len(members) == 0 {
		return nil, fmt.Errorf("exec team of run %s: needs a workspace handle, a run folder and members", runID)
	}
	if rc.Session != nil || utils.TaskRunsAsRoot(rc.Task) || snapshotMode(rc) {
		return nil, fmt.Errorf("exec team of run %s: team runs do not support sessions, root tasks or snapshot grading", runID)
	}
	e.ensureEventCollector(ctx, rc, members[0].Plan, expID, runID)
	defer e.copyAgentEvents(context.WithoutCancel(ctx), runID, rc.Dir)
	if e.cfg.ProcessWatch || e.cfg.WorkspaceWatch {
		off := &stepObservers{e: e}
		off.writeOff(rc, teamObserverReason)
	}
	names := make([]string, len(members))
	for i := range members {
		names[i] = memberContainer(runID, members[i].Member.ID)
	}
	defer e.removeContainers(context.WithoutCancel(ctx), names...)
	for i, m := range members {
		if err := e.createMember(ctx, rc, expID, runID, names[i], m); err != nil {
			return nil, fmt.Errorf("exec team of run %s: member %s: %w", runID, m.Member.ID, err)
		}
	}
	results := make([]schemas.MemberResult, len(members))
	var wg sync.WaitGroup
	for i, m := range members {
		wg.Add(1)
		go func(i int, m schemas.MemberRun) {
			defer wg.Done()
			results[i] = e.attachMember(ctx, rc, names[i], m)
		}(i, m)
	}
	wg.Wait()
	if err := concatMemberStreams(rc.Dir, members); err != nil {
		return results, fmt.Errorf("exec team of run %s: %w", runID, err)
	}
	return results, ctx.Err()
}

// createMember creates one member's container with the same hardening,
// mounts and networks as a single agent's, and joins it to the service
// network where the tool proxy and the mailbox are.
func (e *dockerWorkspace) createMember(ctx context.Context, rc *schemas.RunContext, expID, runID, name string, m schemas.MemberRun) error {
	plan := m.Plan
	if plan == nil {
		return errors.New("no plan")
	}
	if err := e.checkPlan(plan, false); err != nil {
		return err
	}
	if err := e.checkFrozenMemberImage(ctx, rc, m.Member.ID, plan.Image); err != nil {
		return err
	}
	basePath, err := e.imagePath(ctx, plan)
	if err != nil {
		return err
	}
	mounts, links, err := e.agentMounts(ctx, rc, plan, expID, runID)
	if err != nil {
		return err
	}
	l, err := buildLaunch(plan, shimDir+":"+basePath, agentHome, links)
	if err != nil {
		return err
	}
	l.Env = e.eventEnv(rc, runID, l.Env)
	envFile, err := writeEnvFile(l.Env)
	if err != nil {
		return err
	}
	defer func() {
		if rerr := os.Remove(envFile); rerr != nil {
			e.cfg.Logger.Warn("remove env file", "path", envFile, "error", rerr)
		}
	}()
	network, svcNet := e.network(plan.Network), ""
	if len(rc.Services) > 0 || collectorRunning(runID) {
		svcNet = serviceNetwork(runID)
		if network == "none" {
			network, svcNet = svcNet, ""
		}
	}
	e.removeContainers(ctx, name)
	args := agentCreateArgs(name, utils.LabelArgs(expID, runID), plan, rc.Handle, network, envFile, l, mounts, false)
	if _, err := e.cfg.Docker.MustRun(ctx, args...); err != nil {
		return fmt.Errorf("create member container: %w", err)
	}
	if svcNet != "" {
		if _, err := e.cfg.Docker.MustRun(ctx, "network", "connect", svcNet, name); err != nil {
			return fmt.Errorf("join service network: %w", err)
		}
	}
	return nil
}

// attachMember starts one member and waits for it within the plan's wall
// budget. Its own key and the run key are redacted from its output.
func (e *dockerWorkspace) attachMember(ctx context.Context, rc *schemas.RunContext, name string, m schemas.MemberRun) schemas.MemberResult {
	res := schemas.MemberResult{AgentID: m.Member.ID, Container: name}
	wall := time.Duration(m.Plan.WallS) * time.Second
	if m.Plan.WallS <= 0 {
		wall = defaultWallS * time.Second
	}
	secrets := []string{m.Key.Value, m.RouteToken}
	if rc.Key != nil {
		secrets = append(secrets, rc.Key.Value)
	}
	stdout, stderr := memberStreamPaths(rc.Dir, m.Member.ID)
	x, err := e.attach(ctx, name, stdout, stderr, wall, secrets...)
	res.Exec = x
	if err != nil {
		res.Error = err.Error()
	}
	return res
}

// MemberStreamFile is the run folder file holding one member's output.
func MemberStreamFile(id string) string { return "agent." + id + ".stream.jsonl" }

func memberStreamPaths(dir, id string) (string, string) {
	return filepath.Join(dir, MemberStreamFile(id)), filepath.Join(dir, "agent."+id+".stderr.txt")
}

func memberContainer(runID, id string) string { return agentContainer(runID) + "-m-" + id }

// concatMemberStreams writes every member's output, in member order, to the
// run's agent.stream.jsonl and agent.stderr.txt, so parsers and the stdout
// event channel read the whole team.
func concatMemberStreams(dir string, members []schemas.MemberRun) error {
	for k, name := range []string{"agent.stream.jsonl", "agent.stderr.txt"} {
		var out []byte
		for _, m := range members {
			stdout, stderr := memberStreamPaths(dir, m.Member.ID)
			src := []string{stdout, stderr}[k]
			b, err := os.ReadFile(src)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return fmt.Errorf("read %s: %w", filepath.Base(src), err)
			}
			out = append(out, b...)
			if len(b) > 0 && b[len(b)-1] != '\n' {
				out = append(out, '\n')
			}
		}
		if err := os.WriteFile(filepath.Join(dir, name), out, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

// checkFrozenMemberImage refuses a member whose own image tag no longer
// names the image id the manifest froze for it (ArmManifest.Members), as
// checkFrozenImage does for the arm. A member without a frozen id is not
// checked here: it runs the arm's image, which Create checked.
func (e *dockerWorkspace) checkFrozenMemberImage(ctx context.Context, rc *schemas.RunContext, id, image string) error {
	want := frozenMemberImageID(rc, id)
	if want == "" || image == "" {
		return nil
	}
	got, err := e.ImageID(ctx, image)
	if err != nil {
		return fmt.Errorf("member image: %w", err)
	}
	if got != want {
		return fmt.Errorf("member image %s is now %s but experiment %s was planned with %s; restore that image or plan a new experiment",
			image, short(got), rc.Manifest.ExpID, short(want))
	}
	return nil
}

// frozenMemberImageID is the image id the manifest recorded for a member's
// own config in the run's arm.
func frozenMemberImageID(rc *schemas.RunContext, id string) string {
	if rc == nil || rc.Manifest == nil || rc.Slot == nil {
		return ""
	}
	if rc.Slot.Arm == compatArm {
		if rc.Manifest.Compatibility == nil {
			return ""
		}
		return rc.Manifest.Compatibility.Members[id].ImageID
	}
	return rc.Manifest.Arms[rc.Slot.Arm].Members[id].ImageID
}
