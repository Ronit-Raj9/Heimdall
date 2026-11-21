package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/a2a"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// B1: a2a runs (docs/a2a-agents.md). Before the agent starts the engine
// registers the run's A2A agents in Bifrost's agent gateway (one
// run-scoped registration per agent, granted only to the run's keys); after
// it exits the agent log becomes the run's witness (Evidence.A2A); at
// cleanup the registrations are deleted. The adapter (adapters/a2a.go) only
// plans the launcher.

// a2aWitnessWait is how long collect waits for Bifrost's asynchronous log
// writer to hold every operation the launcher made.
var a2aWitnessWait = 30 * time.Second

// checkA2AArm refuses at plan time what an a2a run could not do: a bad a2a
// block, a team on a gateway-registered agent, and the a2a probe layer on an
// arm that is not an a2a agent or does not stream (the fault is a stream
// fault). It is pure.
func checkA2AArm(cfg *schemas.AgentConfig, conds []condPlan) error {
	a2aArm := utils.IsA2A(cfg)
	if a2aArm {
		if cfg.Adapter != "a2a" {
			return fmt.Errorf("an a2a block needs adapter a2a, not %q", cfg.Adapter)
		}
		if err := utils.CheckA2AAgent(cfg.A2A); err != nil {
			return err
		}
		if len(cfg.Team) > 0 {
			return errors.New("an a2a agent declares its team under a2a.team (A2A agents delegate through the gateway); team: starts one container per member")
		}
	}
	for _, c := range conds {
		if c.probe == nil || c.probe.Layer != schemas.LayerA2A {
			continue
		}
		f := c.probe.Apply.A2A
		switch {
		case f == nil:
			return fmt.Errorf("probe %s: layer a2a needs apply.a2a", c.probe.ID)
		case f.Stream != "drop" && f.Stream != "delay":
			return fmt.Errorf("probe %s: apply.a2a.stream %q: want drop or delay", c.probe.ID, f.Stream)
		case !a2aArm:
			return fmt.Errorf("probe %s perturbs an A2A stream, but the arm is not an a2a agent", c.probe.ID)
		case !cfg.A2A.Streaming:
			return fmt.Errorf("probe %s perturbs an A2A stream, but the arm does not stream (a2a.streaming: true)", c.probe.ID)
		}
	}
	return nil
}

// a2aTaskErrs refuses each a2a arm that cannot produce task t's output: an
// A2A agent is not beside the workspace, so it runs answer tasks, state
// tasks only with a2a.tools expose and something to publish, artifact tasks
// only with a2a.artifacts inline, and never diff tasks
// (utils.A2AOutputAllowed). It is pure.
func a2aTaskErrs(suite *schemas.Suite, t *schemas.Task, arms map[string]schemas.ArmManifest, compat *schemas.ArmManifest) []error {
	kind := utils.OutputKind(suite, t)
	var errs []error
	check := func(id string, cfg *schemas.AgentConfig) {
		if !utils.IsA2A(cfg) || cfg.A2A == nil {
			return
		}
		if ok, why := utils.A2AOutputAllowed(cfg.A2A, kind); !ok {
			errs = append(errs, fmt.Errorf("task %s has output %s, but arm %s runs an A2A agent: %s", t.ID, kind, id, why))
			return
		}
		if kind == schemas.OutputState && !hasPublishable(suite, t) {
			errs = append(errs, fmt.Errorf("task %s has output state, but has no proxied service or external API that arm %s's A2A agent could change", t.ID, id))
		}
	}
	for _, id := range sortedKeys(arms) {
		am := arms[id]
		check(id, &am.Config)
	}
	if compat != nil {
		check(CompatArm, &compat.Config)
	}
	return errs
}

// prepareA2A registers the run's agents. It runs at the end of prepare,
// after the run key exists. A registration that cannot be made fails the
// run before the agent starts.
func (p *Pipeline) prepareA2A(ctx context.Context, st *runState) error {
	if !utils.IsA2A(st.rc.Agent) || st.rc.Agent.A2A == nil {
		return nil
	}
	gw, ok := schemas.GatewayAs[schemas.A2AGateway](p.reg.Gateway)
	if !ok {
		return wrapPart("gateway", p.reg.Gateway, errors.New("the gateway has no agent gateway (a2a runs need Bifrost's agent gateway)"))
	}
	spec, rc := st.rc.Agent.A2A, st.rc
	run := &schemas.A2ARun{Members: map[string]string{}, KeyMember: map[string]string{rc.Key.ID: a2a.Lead}, CardURLs: map[string]string{}, Since: p.now().UTC()}
	rc.A2A = run
	if spec.Registration != "" {
		info, err := gw.GetA2A(ctx, spec.Registration)
		if err != nil {
			return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("a2a: %w", err))
		}
		switch {
		case !info.Enabled:
			return &partError{part: "gateway", err: fmt.Errorf("a2a: registration %s is disabled", spec.Registration)}
		case !info.AllowByDefault && !slices.Contains(info.VirtualKeyIDs, rc.Key.ID):
			return &partError{part: "gateway", err: fmt.Errorf("a2a: registration %s does not allow any virtual key, and the bench never edits a registration it did not make; set a2a.card_url so the bench registers the agent per run, or enable allow_by_default", spec.Registration)}
		}
		run.Entry = spec.Registration
		run.Members[run.Entry] = a2a.Lead
		run.CardURLs[run.Entry] = info.CardURL
		return nil
	}
	if len(spec.Team) > 0 {
		if err := p.mintA2ATeam(ctx, st); err != nil {
			return err
		}
	}
	forward := spec.Credential != schemas.A2ACredentialNone
	entry := utils.A2ARegistrationName(rc.Run.RunID, a2a.Lead)
	callers := []string{rc.Key.ID}
	for _, k := range rc.Key.Members {
		if k.ID != rc.Key.ID {
			callers = append(callers, k.ID)
		}
	}
	// 3-A-04: a name is owned before its POST, so a registration created
	// behind a lost or failed answer is still deleted at cleanup (DELETE of
	// a missing registration is not an error). Names are run-scoped.
	run.Owned = append(run.Owned, entry)
	if err := gw.RegisterA2A(ctx, schemas.A2ARegistrationSpec{Name: entry, CardURL: spec.CardURL, VirtualKeyIDs: []string{rc.Key.ID}, Forward: forward}); err != nil {
		return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("a2a: %w", err))
	}
	run.Entry, run.Members[entry], run.CardURLs[entry] = entry, a2a.Lead, spec.CardURL
	for _, m := range spec.Team {
		name := utils.A2ARegistrationName(rc.Run.RunID, m.ID)
		var key *schemas.MemberKey
		for i := range rc.Key.Members {
			if rc.Key.Members[i].AgentID == m.ID {
				key = &rc.Key.Members[i]
			}
		}
		if key == nil {
			return &partError{part: "gateway", err: fmt.Errorf("a2a: team member %s has no key", m.ID)}
		}
		// Every member key may call every other member; the member's own
		// key reaches only its own agent, as its runtime credential.
		var allowed []string
		for _, id := range callers {
			if id != key.ID {
				allowed = append(allowed, id)
			}
		}
		run.Owned = append(run.Owned, name) // 3-A-04: owned before the POST
		if err := gw.RegisterA2A(ctx, schemas.A2ARegistrationSpec{Name: name, CardURL: m.CardURL, VirtualKeyIDs: allowed, RuntimeKey: key.Value}); err != nil {
			return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("a2a: team member %s: %w", m.ID, err))
		}
		run.Members[name] = m.ID
		run.KeyMember[key.ID] = m.ID
		run.CardURLs[name] = m.CardURL
	}
	return nil
}

// mintA2ATeam gives every team member its own key on its own slot (the
// multi-agent area's member keys, team.go), with the lead on the run key,
// and binds the run's fault plan to each member slot so the fault proxy
// tags their calls with the run.
func (p *Pipeline) mintA2ATeam(ctx context.Context, st *runState) error {
	tg, ok := schemas.GatewayAs[schemas.TeamGateway](p.reg.Gateway)
	if !ok {
		return wrapPart("gateway", p.reg.Gateway, errNoTeamGateway)
	}
	members := utils.A2ATeam(st.rc.Agent) // shared with the gateway probe layer's role targets (audit 8-B-12)
	if len(members) == 0 {
		members = []schemas.TeamMember{{ID: a2a.Lead, Role: "lead"}}
	}
	keys, err := tg.MintMemberKeys(ctx, st.rc, st.rc.Key, members)
	if err != nil {
		return wrapPart("gateway", p.reg.Gateway, fmt.Errorf("a2a: mint member keys: %w", err))
	}
	st.rc.Key.Members = keys
	if p.reg.Fault == nil {
		return nil
	}
	for _, k := range keys {
		if k.KeySlot == st.rc.Key.KeySlot {
			continue
		}
		plan := schemas.FaultPlan{Mode: p.runFaultMode(st.rc), RunID: st.rc.Run.RunID}
		if plan.Mode == faultModeScripted {
			if err := directSlot(&schemas.RunKey{KeySlot: k.KeySlot, SlotAuth: k.SlotAuth}); err != nil {
				return &partError{part: faultPart, err: err}
			}
		}
		if err := p.reg.Fault.SetPlan(ctx, k.KeySlot, plan); err != nil {
			return &partError{part: faultPart, err: fmt.Errorf("bind %s plan to member %s slot %s: %w", plan.Mode, k.AgentID, k.KeySlot, err)}
		}
	}
	return nil
}

// collectA2A reads the agent log into Evidence.A2A and adds the
// agent-to-agent hops to Evidence.Messages. It waits for the log until every
// launcher operation is in it, or a2aWitnessWait passes; an incomplete
// witness is kept and marked, never hidden.
func (p *Pipeline) collectA2A(ctx context.Context, st *runState) error {
	rc := st.rc
	if rc.A2A == nil || !utils.IsA2A(rc.Agent) {
		return nil
	}
	gw, ok := schemas.GatewayAs[schemas.A2AGateway](p.reg.Gateway)
	if !ok {
		return nil
	}
	var rep *schemas.A2ALauncherReport
	if rc.Exec != nil && rc.Exec.StdoutPath != "" {
		rep = a2a.ReadLauncherReport(rc.Exec.StdoutPath)
	}
	names := make([]string, 0, len(rc.A2A.Members))
	for n := range rc.A2A.Members {
		names = append(names, n)
	}
	slices.Sort(names)
	deadline := time.Now().Add(a2aWitnessWait)
	var ev *schemas.A2AEvidence
	var rows []schemas.A2ALogRow
	for {
		var fetchErrs []string
		var err error
		rows, err = gw.A2AHistory(ctx, names, rc.A2A.Since)
		if err != nil {
			fetchErrs = append(fetchErrs, err.Error())
		}
		in := a2a.Input{Run: rc.A2A, RunKeyID: rc.Key.ID, Launcher: rep, Rows: rows, FetchErrs: fetchErrs, Fault: utils.A2AFaultOf(rc)}
		ev = a2a.Build(in)
		// Without a launcher report nothing can complete the witness; what
		// the log holds once it holds anything is kept.
		settled := (ev.Complete && (ev.Agreement == nil || ev.Agreement.GatewayState != "" || !rc.Agent.A2A.Streaming)) ||
			(rep == nil && len(ev.Operations) > 0)
		if settled || time.Now().After(deadline) || ctx.Err() != nil {
			var tasks []string
			for _, op := range ev.Operations {
				if op.TaskID != "" && !slices.Contains(tasks, op.TaskID) {
					tasks = append(tasks, op.TaskID)
				}
			}
			linked, lerr := gw.A2ACorrelated(ctx, tasks, rc.A2A.Since)
			if lerr != nil {
				in.FetchErrs = append(in.FetchErrs, lerr.Error())
			}
			in.Linked = linked
			ev = a2a.Build(in)
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	if man := gatewayVersion(st); man != "" {
		ev.Gateway = man
	}
	st.ev.A2A = ev
	st.ev.Messages = append(st.ev.Messages, a2a.Messages(ev, rows)...)
	return nil
}

// gatewayVersion is the gateway version the experiment's preflight
// recorded, or "".
func gatewayVersion(st *runState) string {
	if st.in.Manifest == nil || st.in.Manifest.Gateway == nil {
		return ""
	}
	return st.in.Manifest.Gateway.Name + " " + st.in.Manifest.Gateway.Version
}

// cleanupA2A deletes the registrations the bench made for the run.
func (p *Pipeline) cleanupA2A(ctx context.Context, st *runState) error {
	if st.rc == nil || st.rc.A2A == nil {
		return nil
	}
	gw, ok := schemas.GatewayAs[schemas.A2AGateway](p.reg.Gateway)
	if !ok {
		return nil
	}
	var errs []error
	for _, n := range st.rc.A2A.Owned {
		if err := gw.DeleteA2A(ctx, n); err != nil { // QC2-06-01: ctx is cleanup's detached, bounded context; its deadline is kept
			errs = append(errs, wrapPart("gateway", p.reg.Gateway, fmt.Errorf("a2a cleanup: %w", err)))
		}
	}
	return errors.Join(errs...)
}
