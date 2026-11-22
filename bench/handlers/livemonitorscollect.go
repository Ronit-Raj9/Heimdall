package handlers

// Lane Z22: the monitor collector. CollectMonitors turns every finished
// check of every monitor into a sample, runs the incident rules over it
// and sends the notices through MonitorNotifier. The server ticker calls it
// about once a minute; it is idempotent (a sample per evaluation, a sample
// per failed fire).

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/livemon"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// MonitorCheckTimeout is how long a check may run before it is recorded as
// one that could not finish.
const MonitorCheckTimeout = 6 * time.Hour

// MonitorNotifier sends one incident notice; cmd/bifrost-bench sets it to
// the org's webhook outbox (Slack, PagerDuty, email and webhooks there).
// Nil sends nothing; the incident is still recorded.
var MonitorNotifier func(env OrgEnv, m livemon.Monitor, n livemon.Notice)

// monitorProbe reports whether an agent endpoint answers at all (any HTTP
// status counts). Tests replace it.
var monitorProbe = func(ctx context.Context, hosted bool, rawURL string) bool {
	if err := (&agentTestHandler{env: OrgEnv{Services: Services{Hosted: hosted}}}).checkURL(rawURL); err != nil {
		return false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	resp, err := endpointTestClient(hosted, 8*time.Second).Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// CollectMonitors brings every monitor of env's org up to date and returns
// how many samples it added.
func CollectMonitors(ctx context.Context, env OrgEnv, now time.Time) (int, error) {
	root := env.Cfg.Root
	store := livemon.Open(root)
	ms, err := store.List()
	if err != nil || len(ms) == 0 {
		return 0, err
	}
	evals, err := evalstore.Open(root).List()
	if err != nil {
		return 0, fmt.Errorf("list evaluations: %w", err)
	}
	bySchedule := map[string][]*schemas.Evaluation{}
	for _, ev := range evals {
		if _, ok := livemon.MonitorIDOfSchedule(ev.Schedule); ok {
			bySchedule[ev.Schedule] = append(bySchedule[ev.Schedule], ev)
		}
	}
	last, _ := schedule.LastRecords(ScheduleStateDir(root))
	added := 0
	for _, m := range ms {
		n, err := collectMonitor(ctx, env, store, m, bySchedule[m.ScheduleName()], last[m.ScheduleName()], now)
		added += n
		if err != nil {
			return added, err
		}
	}
	return added, nil
}

func collectMonitor(ctx context.Context, env OrgEnv, store *livemon.Store, m livemon.Monitor, evals []*schemas.Evaluation, rec schedule.Record, now time.Time) (int, error) {
	unlock := store.Lock()
	defer unlock()
	st, err := store.State(m.ID)
	if err != nil {
		return 0, err
	}
	slices.SortFunc(evals, func(a, b *schemas.Evaluation) int { return a.CreatedAt.Compare(b.CreatedAt) })
	var fresh []livemon.Sample
	h := &evaluationsHandler{env: env, now: time.Now}
	for _, ev := range evals {
		if st.HasEvaluation(ev.ID) || ev.CreatedAt.Before(m.CreatedAt.Add(-time.Minute)) {
			continue
		}
		h.refresh(ev)
		if !terminalEvaluation(ev.Status) {
			if now.Sub(ev.CreatedAt) < MonitorCheckTimeout {
				continue
			}
			fresh = append(fresh, livemon.Sample{At: ev.CreatedAt, EvaluationID: ev.ID, Status: livemon.SampleError,
				Reason: fmt.Sprintf("the check did not finish within %s", MonitorCheckTimeout), Runs: []livemon.RunSample{}})
			continue
		}
		fresh = append(fresh, monitorSample(ctx, env, m, ev))
	}
	if !rec.Finished.IsZero() && rec.Finished.After(st.LastRecordAt) {
		st.LastRecordAt = rec.Finished
		if s, ok := recordSample(rec, m); ok {
			fresh = append(fresh, s)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	slices.SortStableFunc(fresh, func(a, b livemon.Sample) int { return a.At.Compare(b.At) })
	var notices []livemon.Notice
	for _, s := range fresh {
		s.Summarize()
		st.Samples = append(st.Samples, s)
		var n []livemon.Notice
		st.Samples, st.Incidents, n = livemon.Step(m, st.Samples, st.Incidents, now)
		notices = append(notices, n...)
	}
	if err := store.SaveState(m.ID, st); err != nil {
		return 0, err
	}
	if f := MonitorNotifier; f != nil {
		for _, n := range notices {
			f(env, m, n)
		}
	}
	return len(fresh), nil
}

// recordSample is the sample of a fire that started no evaluation: a
// refusal for the daily budget is skipped, anything else could not run.
func recordSample(rec schedule.Record, m livemon.Monitor) (livemon.Sample, bool) {
	if rec.Result != nil && rec.Result.EvaluationID != "" {
		return livemon.Sample{}, false // the evaluation's own sample covers it
	}
	at := rec.At
	if at.IsZero() {
		at = rec.Started
	}
	s := livemon.Sample{At: at, Reason: rec.Reason, Runs: []livemon.RunSample{}}
	switch rec.Status {
	case schedule.StatusSkipped:
		s.Status = livemon.SampleSkipped
	case schedule.StatusRefused:
		s.Status = livemon.SampleError
		if strings.Contains(rec.Reason, "daily cap") {
			s.Status = livemon.SampleSkipped
			s.Reason = fmt.Sprintf("skipped: the monitor's daily budget of %.2f USD is spent", m.BudgetUSDPerDay)
		}
	case schedule.StatusError, schedule.StatusFailed:
		s.Status = livemon.SampleError
	default:
		return livemon.Sample{}, false
	}
	return s, true
}

// monitorSample reads a finished check's runs: the newest attempt of each
// counted clean slot.
func monitorSample(ctx context.Context, env OrgEnv, m livemon.Monitor, ev *schemas.Evaluation) livemon.Sample {
	s := livemon.Sample{At: ev.CreatedAt, EvaluationID: ev.ID, Runs: []livemon.RunSample{}}
	if ev.Status == schemas.EvaluationCancelled {
		s.Status, s.Reason = livemon.SampleSkipped, "the check was cancelled"
		return s
	}
	var reasons []string
	idx := env.Services.Index
	for _, c := range ev.Children {
		if c.ExperimentID == "" || c.Status == schemas.EvaluationChildSkipped {
			if c.Skipped != "" {
				reasons = append(reasons, c.Skipped)
			}
			continue
		}
		s.ExperimentID = c.ExperimentID
		if idx == nil {
			continue
		}
		runs := idx.Runs(c.ExperimentID)
		costs := runCosts(idx, runs)
		newest := map[string]RunView{}
		for _, rv := range runs {
			if !rv.Slot.Counted || rv.Superseded || !rv.Sealed || rv.Slot.Condition != schemas.ConditionClean {
				continue
			}
			if old, ok := newest[rv.Slot.SlotID]; !ok || rv.Run.Attempt > old.Run.Attempt {
				newest[rv.Slot.SlotID] = rv
			}
		}
		keys := make([]string, 0, len(newest))
		for k := range newest {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			s.Runs = append(s.Runs, monitorRun(newest[k], costs))
		}
	}
	if len(s.Runs) == 0 {
		s.Status = livemon.SampleError
		s.Reason = strings.Join(reasons, "; ")
		if s.Reason == "" {
			s.Reason = "the check finished with no run"
		}
	}
	s.Summarize()
	if s.Status == livemon.SampleError {
		return s
	}
	if hasFailures(s) {
		if u := monitorEndpoint(ctx, env, m.Agent); u != "" {
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			ok := monitorProbe(pctx, env.Services.Hosted, u)
			cancel()
			s.Reachable = &ok
			if !ok {
				for i := range s.Runs {
					if s.Runs[i].Pass == nil || !*s.Runs[i].Pass {
						s.Runs[i].Infra, s.Runs[i].Pass = true, nil
					}
				}
			}
		}
	}
	return s
}

func hasFailures(s livemon.Sample) bool {
	for _, r := range s.Runs {
		if r.Infra || r.Pass == nil || !*r.Pass {
			return true
		}
	}
	return false
}

// monitorRun is one sealed run as a sample run. A run that ended for an
// infrastructure reason (the bench, the gateway or the agent's endpoint
// failing) has no pass value, so it never counts as a wrong answer.
func monitorRun(rv RunView, costs map[string]schemas.Value) livemon.RunSample {
	r := rv.Run
	out := livemon.RunSample{RunID: r.RunID, Task: rv.Slot.Task, Outcome: string(r.Outcome)}
	for _, f := range r.Failures {
		if f.Error != "" {
			out.Error = etCap(f.Error, 300)
			break
		}
	}
	y, scored := r.Outcome.Score()
	switch {
	case r.Outcome == schemas.OutcomeInfraError || r.Outcome == schemas.OutcomeAbortedInfra || r.Agent.OOMKilled:
		out.Infra = true
	case (r.Outcome == schemas.OutcomeCrash || r.Outcome == schemas.OutcomeTimeout) && livemon.InfraError(out.Error):
		out.Infra = true
	case scored:
		p := y == 1
		out.Pass = &p
	}
	if t := r.Timing; t.AgentStarted != nil && t.AgentExited != nil && t.AgentExited.After(*t.AgentStarted) {
		ms := t.AgentExited.Sub(*t.AgentStarted).Milliseconds()
		out.LatencyMS = &ms
	} else if t.Sealed != nil && !t.Started.IsZero() && t.Sealed.After(t.Started) {
		ms := t.Sealed.Sub(t.Started).Milliseconds()
		out.LatencyMS = &ms
	}
	if v, ok := costs[r.RunID]; ok && v.State == schemas.ValueObserved && v.V != nil {
		c := *v.V
		out.CostUSD = &c
	}
	return out
}

// monitorEndpoint is the URL a direct reachability check calls: the HTTP
// or OpenAI-compatible endpoint of the agent; "" for agents Heimdall
// starts itself.
func monitorEndpoint(ctx context.Context, env OrgEnv, agent string) string {
	cfg, err := monitorAgentConfig(ctx, env, agent)
	if err != nil || cfg == nil {
		return ""
	}
	switch {
	case cfg.Remote != nil && cfg.Remote.URL != "":
		return cfg.Remote.URL
	case cfg.OpenAI != nil && cfg.OpenAI.URL != "":
		return cfg.OpenAI.URL
	}
	return ""
}
