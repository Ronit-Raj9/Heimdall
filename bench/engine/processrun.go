package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// processPoll is how often the process probe's watcher counts the run's
// model calls.
var processPoll = 250 * time.Millisecond

// minResumeWallS is the least wall budget a resumed agent gets.
const minResumeWallS = 60

// processRule is the process probe's rule when it applies to this Exec:
// the run's probe is on the process layer, the agent was not killed yet,
// and this is the session the rule names (the first when it names none).
func processRule(st *runState, sess *schemas.SessionSpec) *schemas.ProcessFault {
	rc := st.rc
	if st.killed || rc.Probe == nil || rc.Probe.Layer != schemas.LayerProcess || rc.Probe.Apply.Process == nil {
		return nil
	}
	rule := rc.Probe.Apply.Process
	switch {
	case rule.Session == "":
		if sess != nil && rc.SessionIndex != 0 {
			return nil
		}
	case sess == nil || sess.ID != rule.Session:
		return nil
	}
	return rule
}

// execAgent runs the agent once, or, under a process probe, runs it until
// the rule's trigger, SIGKILLs it, and runs it again on the same workspace
// and memory with BENCH_RESUME=1 and the wall budget it had left. The two
// outputs are kept as one stream, the first before the second. The kill is
// recorded in process_kill.json and Evidence.ProcessKill.
func (p *Pipeline) execAgent(ctx context.Context, st *runState, plan *schemas.RunPlan, sess *schemas.SessionSpec) (*schemas.ExecResult, error) {
	rule := processRule(st, sess)
	if rule == nil {
		return st.env.Exec(ctx, st.rc, plan)
	}
	if err := rule.Validate(); err != nil {
		return nil, err
	}
	killer, ok := st.env.(schemas.AgentKiller)
	if !ok {
		return nil, wrapPart("environment", st.env, fmt.Errorf("process probe: environment %s cannot kill the agent", st.env.GetName()))
	}
	started := p.now()
	wctx, cancel := context.WithCancel(ctx)
	var (
		wg   sync.WaitGroup
		kill *schemas.ProcessKill
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		kill = p.watchProcess(wctx, st, rule, killer, started)
	}()
	res, err := st.env.Exec(ctx, st.rc, plan)
	cancel()
	wg.Wait()
	if kill == nil {
		return res, err
	}
	st.killed = true
	if sess != nil {
		kill.Session = sess.ID
	}
	if res != nil {
		kill.ExitCode = res.ExitCode
	}
	if err != nil {
		kill.ResumeError = "the killed run failed before it could resume: " + err.Error()
		p.recordKill(ctx, st, kill)
		return res, err
	}
	var first []byte
	if res != nil && res.StdoutPath != "" {
		first, _ = os.ReadFile(res.StdoutPath)
	}
	again := *plan
	again.Env = maps.Clone(plan.Env)
	if again.Env == nil {
		again.Env = map[string]string{}
	}
	again.Env[schemas.EnvResume] = "1"
	if plan.WallS > 0 {
		left := plan.WallS - int(p.now().Sub(started).Seconds())
		again.WallS = max(left, minResumeWallS)
	}
	res2, err2 := st.env.Exec(ctx, st.rc, &again)
	if res2 != nil {
		code := res2.ExitCode
		kill.Resumed, kill.ResumeExit = true, &code
		if len(first) > 0 && res2.StdoutPath != "" {
			if second, rerr := os.ReadFile(res2.StdoutPath); rerr == nil || errors.Is(rerr, os.ErrNotExist) {
				_ = os.WriteFile(res2.StdoutPath, append(first, second...), 0o644)
			}
		}
		if !res.Started.IsZero() {
			res2.Started = res.Started
		}
	}
	if err2 != nil {
		kill.ResumeError = err2.Error()
	}
	p.recordKill(ctx, st, kill)
	return res2, err2
}

// watchProcess waits for the rule's trigger and kills the agent: AfterS
// seconds after start, or once the fault proxy's ledger holds AtModelCall
// model calls of the run, whichever comes first. It returns nil when ctx
// ended first (the agent exited before the trigger) or the kill failed.
func (p *Pipeline) watchProcess(ctx context.Context, st *runState, rule *schemas.ProcessFault, killer schemas.AgentKiller, started time.Time) *schemas.ProcessKill {
	t := time.NewTicker(processPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
		calls := p.modelCalls(ctx, st.rc)
		trigger := ""
		switch {
		case rule.AtModelCall > 0 && calls >= rule.AtModelCall:
			trigger = "model_call"
		case rule.AfterS > 0 && p.now().Sub(started) >= time.Duration(rule.AfterS)*time.Second:
			trigger = "after_s"
		}
		if trigger == "" {
			continue
		}
		at := p.now().UTC()
		if err := killer.KillAgent(context.WithoutCancel(ctx), st.rc); err != nil {
			p.logger.Warn("process probe: kill failed; the run goes on unkilled", "run", st.rc.Run.RunID, "error", err.Error())
			return nil
		}
		return &schemas.ProcessKill{KilledAt: at, ModelCalls: calls, Trigger: trigger}
	}
}

// modelCalls counts the run's conversation requests in the fault proxy's
// ledger; 0 without a proxy or when the ledger cannot be read.
func (p *Pipeline) modelCalls(ctx context.Context, rc *schemas.RunContext) int {
	if p.reg.Fault == nil || rc.Run == nil {
		return 0
	}
	entries, err := p.reg.Fault.Ledger(ctx, rc.Run.RunID)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Kind == "message" {
			n++
		}
	}
	return n
}

// recordKill keeps the kill on the evidence and in process_kill.json.
func (p *Pipeline) recordKill(ctx context.Context, st *runState, kill *schemas.ProcessKill) {
	st.ev.ProcessKill = kill
	if err := p.writeJSON(ctx, st, schemas.ProcessKillFile, kill); err != nil {
		p.logger.Warn("process probe: kill record not written", "run", st.rc.Run.RunID, "error", err.Error())
	}
}
