package main

// Gap lane G07: blame.replay, the web form of `blame -replay`. For each
// blamed team failure it plans the tier-2 child experiment (the same slot as
// a control arm and with the suspect swapped for a stub agent), freezes and
// starts it through the org's Launcher (hosted: on the org's runners, never
// in the server process), waits for it to end, judges its two runs and
// writes the result into the run's blame.json.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/attribution"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	kindBlameReplay = "blame.replay"
	maxBlameReplays = 10
)

// blameReplayParams are blame.replay's params.
type blameReplayParams struct {
	ExpID string `json:"exp_id"`
	RunID string `json:"run_id,omitempty"` // only this run
	Stub  string `json:"stub,omitempty"`   // agent config replacing the suspect; default attribution.stub
	Max   int    `json:"max,omitempty"`    // most replays; default attribution.max_replays, else 3
}

// blameReplayItem is one run's replay in the result.
type blameReplayItem struct {
	RunID    string `json:"run_id"`
	ReplayID string `json:"replay_exp_id,omitempty"`
	Status   string `json:"status"`
	Tier     string `json:"tier"`
	Agent    string `json:"agent,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// blameReplayResult is blame.replay's result.
type blameReplayResult struct {
	ExpID      string            `json:"exp_id"`
	Candidates int               `json:"candidates"` // blamed team failures a replay can test
	Replays    []blameReplayItem `json:"replays"`
}

// replayPoll is how often blame.replay checks whether a replay ended, and
// replayTimeout how long one replay may take before it is cancelled.
var (
	replayPoll    = 2 * time.Second
	replayTimeout = 2 * time.Hour
)

func init() {
	registerJobKind(jobKindSpec{
		Kind: kindBlameReplay, Local: true, Hosted: true,
		Opts:     []jobs.KindOption{jobs.WithMaxAttempts(1)},
		Validate: validateBlameReplayJob,
		Run:      runBlameReplayJob,
	})
}

func validateBlameReplayJob(ctx context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p blameReplayParams
	if err := strictParams(orEmpty(raw), &p); err != nil {
		return nil, "", err
	}
	if p.ExpID == "" {
		return nil, "", invalid(errors.New("exp_id is required"))
	}
	if p.RunID != "" {
		if err := ops.ValidID("run_id", p.RunID); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Stub != "" {
		if _, err := ops.Confine(env.App.cfg.Root, "stub", p.Stub); err != nil {
			return nil, "", invalid(err)
		}
	}
	if p.Max < 0 || p.Max > maxBlameReplays {
		return nil, "", invalid(fmt.Errorf("max must be between 1 and %d", maxBlameReplays))
	}
	m, err := validateParentExp(ctx, env, p.ExpID)
	if err != nil {
		return nil, "", err
	}
	if spec := replaySpec(m.Experiment.Attribution, true, p.Stub, p.Max); !spec.Replay {
		return nil, "", invalid(errors.New("no stub agent to replay with: pick one (stub, an agents/<id>.yaml that replaces the blamed member) or set attribution.stub in the experiment file"))
	}
	return p, analysisTarget(p.ExpID), nil
}

func runBlameReplayJob(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p blameReplayParams
	if err := j.DecodeParams(&p); err != nil {
		return nil, err
	}
	a, l := env.App, env.Services.Launcher
	if l == nil {
		return nil, errors.New("this server has no experiment launcher; start it with a database (serve) to replay runs from the web")
	}
	m, err := a.store.LoadManifest(ctx, p.ExpID)
	if err != nil {
		return nil, err
	}
	spec := replaySpec(m.Experiment.Attribution, true, p.Stub, p.Max)
	runs, err := a.store.SealedRuns(ctx, p.ExpID, "")
	if err != nil {
		return nil, fmt.Errorf("load sealed runs: %w", err)
	}
	root := a.store.Root()
	res := blameReplayResult{ExpID: p.ExpID, Replays: []blameReplayItem{}}
	seen := false
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sr := &runs[i]
		if p.RunID != "" && sr.Run.RunID != p.RunID {
			continue
		}
		seen = true
		if len(sr.Problems) > 0 {
			log.Printf("%s: skipped, evidence problem: %s", sr.Run.RunID, sr.Problems[0])
			continue
		}
		b, ev, err := tier1Of(sr)
		if err != nil {
			return nil, fmt.Errorf("blame %s: %w", sr.Run.RunID, err)
		}
		if !attribution.ReplayCandidate(b, ev) {
			continue
		}
		res.Candidates++
		if len(res.Replays) >= spec.MaxReplays {
			continue
		}
		progress(map[string]any{"phase": "replaying", "run_id": sr.Run.RunID, "done": len(res.Replays)})
		item := blameReplayItem{RunID: sr.Run.RunID}
		replayID, rerr := replayViaLauncher(ctx, env, l, m, sr, b, spec.Stub, log)
		item.ReplayID = replayID
		if rerr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Printf("%s: replay failed, blame kept at tier 1: %v", sr.Run.RunID, rerr)
			b.Replay = &attribution.Replay{Status: attribution.ReplayError, Suspect: b.AgentID, Stub: spec.Stub, ExpID: replayID, OutcomeBefore: b.Outcome, Detail: rerr.Error()}
		}
		if err := attribution.Write(root, b); err != nil {
			return nil, fmt.Errorf("blame %s: %w", sr.Run.RunID, err)
		}
		item.Status, item.Tier, item.Agent = string(b.Replay.Status), string(b.Tier), b.AgentID
		item.Detail = b.Replay.Detail
		res.Replays = append(res.Replays, item)
		log.Printf("%s: replay %s, tier %s, agent %s", sr.Run.RunID, item.Status, item.Tier, orDash(item.Agent))
	}
	if p.RunID != "" && !seen {
		return nil, fmt.Errorf("%w: run %s is not a sealed run of %s", jobs.ErrNotFound, p.RunID, p.ExpID)
	}
	if res.Candidates == 0 {
		log.Printf("no blamed team failure to replay: tier 2 tests a failure tier 1 pinned on one team member")
	}
	log.Printf("%d replay candidates, %d replayed (stub %s)", res.Candidates, len(res.Replays), spec.Stub)
	return res, nil
}

// replayViaLauncher plans, freezes and starts the replay of one run through
// the launcher, waits for it to end and judges it into b. It returns the
// replay experiment id once one was planned.
func replayViaLauncher(ctx context.Context, env jobEnv, l handlers.Launcher, m *schemas.Manifest, sr *schemas.SealedRun, b *attribution.Blame, stub string, log jobs.Logger) (string, error) {
	a := env.App
	exp, err := a.replayExperiment(ctx, m, sr, b, stub)
	if err != nil {
		return "", err
	}
	rm, err := l.Plan(ctx, exp)
	if err != nil {
		return "", fmt.Errorf("plan replay: %w", err)
	}
	if err := l.Create(ctx, rm); err != nil {
		return rm.ExpID, fmt.Errorf("freeze replay: %w", err)
	}
	if err := l.Start(ctx, rm.ExpID); err != nil {
		return rm.ExpID, fmt.Errorf("start replay: %w", err)
	}
	log.Printf("%s: replay %s started (suspect %s swapped for %s)", sr.Run.RunID, rm.ExpID, orDash(b.AgentID), stub)
	if err := waitExperiment(ctx, l, rm.ExpID, replayTimeout, replayPoll, log); err != nil {
		return rm.ExpID, err
	}
	return rm.ExpID, a.judgeReplay(ctx, rm.ExpID, b, stub)
}
