package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("blame", "which agent of each multi-agent run caused its failure: tier 1 from sealed gates, tier 2 by opt-in replay; writes blame.json child artifacts", runBlameCmd)
}

// runBlameCmd computes blame.json for every sealed run of an experiment
// (docs/multi-agent-safety.md). Tier 1 is free and always computed. Tier 2
// replays runs again, so it runs only when the experiment opted in
// (attribution.replay) or -replay is given, and at most max_replays times.
func runBlameCmd(args []string) int {
	fs := flag.NewFlagSet("blame", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id")
	only := fs.String("run", "", "only this run id")
	replay := fs.Bool("replay", false, "replay blamed team failures (tier 2) even when the experiment did not opt in; runs agents again")
	stub := fs.String("stub", "", "agent config that replaces the blamed member in a replay (default: the experiment's attribution.stub)")
	maxReplays := fs.Int("max", 0, "most replays to run (default: attribution.max_replays, else 3)")
	noReplay := fs.Bool("no-replay", false, "tier 1 only, even when the experiment opted in to replay")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench blame [-config deploy/bench.yaml] -exp <exp_id> [-run <run_id>] [-replay | -no-replay] [-stub agents/x.yaml] [-max n]")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, err := loadManifestOnly(ctx, *config, *exp)
	if err != nil {
		return fail("%v", err)
	}
	spec := replaySpec(m.Experiment.Attribution, *replay, *stub, *maxReplays)
	if *noReplay {
		spec.Replay = false
	}
	a, err := newApp(ctx, *config, appOptions{gateway: spec.Replay})
	if err != nil {
		return fail("%v", err)
	}
	runs, err := a.store.SealedRuns(ctx, *exp, "")
	if err != nil {
		return fail("blame %s: %v", *exp, err)
	}
	fmt.Printf("%-16s %-20s %-17s %-12s %s\n", "run", "outcome", "tier", "agent", "gate")
	replays := 0
	for i := range runs {
		sr := &runs[i]
		if *only != "" && sr.Run.RunID != *only {
			continue
		}
		b, ev, err := tier1Of(sr)
		if err != nil {
			return fail("blame %s: %v", sr.Run.RunID, err)
		}
		if spec.Replay && replays < spec.MaxReplays && attribution.ReplayCandidate(b, ev) {
			replays++
			if err := a.replayBlame(ctx, m, sr, b, spec.Stub); err != nil {
				a.logger.Warn("replay failed; blame kept at tier 1", "run_id", sr.Run.RunID, "error", err.Error())
				b.Replay = &attribution.Replay{Status: attribution.ReplayError, Suspect: b.AgentID, Stub: spec.Stub, OutcomeBefore: b.Outcome, Detail: err.Error()}
			}
		}
		keepReplay(a.store.Root(), sr, b)
		if err := attribution.Write(a.store.Root(), b); err != nil {
			return fail("blame %s: %v", sr.Run.RunID, err)
		}
		fmt.Printf("%-16s %-20s %-17s %-12s %s\n", sr.Run.RunID, b.Outcome, b.Tier, orDash(b.AgentID), orDash(b.Gate))
	}
	fmt.Printf("\nblame written to %s/%s/%s/<run_id>/%s; replays run: %d\n", a.store.Root(), *exp, attribution.BlameDir, attribution.BlameFile, replays)
	return exitOK
}

// loadManifestOnly reads an experiment's manifest without the gateway, so a
// tier-1 blame never needs the stack.
func loadManifestOnly(ctx context.Context, config, expID string) (*schemas.Manifest, error) {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return nil, err
	}
	return a.store.LoadManifest(ctx, expID)
}

// replaySpec merges the experiment's attribution block with the flags.
func replaySpec(s *schemas.AttributionSpec, force bool, stub string, max int) schemas.AttributionSpec {
	out := schemas.AttributionSpec{}
	if s != nil {
		out = *s
	}
	out.Replay = out.Replay || force
	if stub != "" {
		out.Stub = stub
	}
	if max > 0 {
		out.MaxReplays = max
	}
	if out.MaxReplays <= 0 {
		out.MaxReplays = attribution.DefaultMaxReplays
	}
	if out.Replay && out.Stub == "" {
		out.Replay = false // no stub to replace the suspect with: tier 1 only
	}
	return out
}

// tier1Of computes the tier-1 blame of one sealed run from its folder.
func tier1Of(sr *schemas.SealedRun) (*attribution.Blame, *schemas.Evidence, error) {
	ev, err := attribution.LoadEvidence(sr.Dir)
	if err != nil {
		return nil, nil, err
	}
	b := attribution.Tier1(attribution.Input{RunID: sr.Run.RunID, ExpID: sr.Run.ExpID, ParentSeal: sr.SealHash,
		Outcome: sr.Run.Outcome, Gates: sr.Gates, Evidence: ev})
	if b.ExpID == "" {
		b.ExpID = sr.Slot.ExpID
	}
	return b, ev, nil
}

// replayBlame plans, freezes and runs the tier-2 child experiment of one
// blamed run, then judges its two runs into b.
func (a *app) replayBlame(ctx context.Context, m *schemas.Manifest, sr *schemas.SealedRun, b *attribution.Blame, stub string) error {
	team := m.Arms[sr.Slot.Arm].Config.Team
	if len(team) == 0 {
		suite, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
		if err != nil {
			return err
		}
		if t := suite.TaskByID(sr.Slot.Task); t != nil {
			team = t.Team
		}
	}
	exp, err := attribution.PlanReplay(&m.Experiment, sr.Slot, team, b, stub)
	if err != nil {
		return err
	}
	exp.Name = "replay " + sr.Run.RunID + " " + utils.NewID("rp", 4)
	rm, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fmt.Errorf("plan replay: %w", err)
	}
	if err := a.store.CreateExperiment(ctx, rm); err != nil {
		return fmt.Errorf("freeze replay: %w", err)
	}
	if code := a.execute(ctx, rm, 1); code != exitOK {
		return fmt.Errorf("replay %s did not finish (exit %d)", rm.ExpID, code)
	}
	runs, err := a.store.SealedRuns(ctx, rm.ExpID, "")
	if err != nil {
		return fmt.Errorf("read replay %s: %w", rm.ExpID, err)
	}
	var control, stubRun *attribution.ReplayRun
	for _, r := range runs {
		rr := &attribution.ReplayRun{RunID: r.Run.RunID, Outcome: r.Run.Outcome, Gates: r.Gates}
		if v, ok := r.Metrics["cost_usd"]; ok {
			if f, ok := v.Float(); ok {
				rr.CostUSD = &f
			}
		}
		switch r.Slot.Arm {
		case attribution.ReplayControlArm:
			control = rr
		case attribution.ReplayStubArm:
			stubRun = rr
		}
	}
	attribution.Judge(b, rm.ExpID, stub, control, stubRun)
	return nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// keepReplay carries an earlier replay of the same sealing of the run into
// b when this pass ran none, so a tier-1 pass never erases a tier-2 result.
func keepReplay(root string, sr *schemas.SealedRun, b *attribution.Blame) {
	if b.Replay != nil {
		return
	}
	old, err := attribution.Read(root, b.ExpID, sr.Run.RunID, sr.SealHash)
	if err != nil || old == nil || old.Replay == nil {
		return
	}
	b.Replay = old.Replay
	if old.Tier == attribution.TierReplayValidated && b.Tier == attribution.TierGateProven && old.AgentID == b.AgentID {
		b.Tier = attribution.TierReplayValidated
	}
}
