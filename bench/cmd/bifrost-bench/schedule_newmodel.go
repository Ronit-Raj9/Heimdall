package main

// W4 feature 3: the new model watch. An evaluation schedule with new_models
// reads Bifrost's newest models on each trigger and starts the schedule's
// evaluation once per model the catalog did not list before, next to the
// setup that is today's model, with its own hard budget.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// watchNow is the clock of the new model watch; tests replace it.
var watchNow = time.Now

// Timing of the new model watch.
const (
	watchLockStale    = 10 * time.Minute
	watchClaimStale   = 30 * time.Minute
	watchFetchTimeout = 10 * time.Second
	watchNewestLimit  = 20
)

// newestModel is one model of Bifrost's GET /api/models/newest.
type newestModel struct {
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Created  int64  `json:"created"`
}

// runNewModelSchedule is runEvaluationSchedule for a schedule with a new
// model watch (schedule.NewModelWatch).
func runNewModelSchedule(ctx context.Context, a *app, svc *handlers.Services, s schedule.Schedule, spec *schemas.EvaluationSpec) (*schedule.Result, error) {
	w := s.NewModels
	root := a.cfg.Root
	path := schedule.WatchStatePath(root, s.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	unlock, busy, err := takeWatchLock(path + ".lock")
	if err != nil {
		return nil, err
	}
	if busy {
		return &schedule.Result{Summary: "another check of this watch is running"}, nil
	}
	defer unlock()

	st, err := schedule.LoadWatchState(root, s.Name)
	if err != nil {
		return nil, err
	}
	now := watchNow()
	models, ferr := fetchNewestModels(ctx, a, w.Providers)
	if ferr != nil {
		if st != nil {
			st.CheckedAt, st.LastError = now, ferr.Error()
			_ = schedule.SaveWatchState(root, st)
		}
		return nil, fmt.Errorf("read the newest models from Bifrost: %w", ferr)
	}
	if st == nil {
		st = &schedule.WatchState{Schedule: s.Name, Baseline: map[string][]string{}, Seen: map[string]schedule.WatchItem{}, CheckedAt: now}
		for _, p := range w.Providers {
			st.Baseline[p] = []string{}
		}
		for _, m := range models {
			st.Baseline[m.Provider] = append(st.Baseline[m.Provider], m.Name)
		}
		if err := schedule.SaveWatchState(root, st); err != nil {
			return nil, err
		}
		return &schedule.Result{Summary: fmt.Sprintf("Watching %s: %d models known. A model that appears after today starts the evaluation.",
			strings.Join(w.Providers, ", "), len(models))}, nil
	}
	st.CheckedAt, st.LastError = now, ""

	// A claim left by a check that died before its launch finished is
	// never started again by itself.
	for k, it := range st.Seen {
		if it.State == schedule.WatchClaimed && now.Sub(it.ClaimedAt) > watchClaimStale {
			it.State, it.Reason = schedule.WatchFailed, "the start was interrupted; press Try again for this model on the Gateway page, then Run now on the schedule"
			st.Seen[k] = it
		}
	}

	known := map[string]bool{}
	for p, ids := range st.Baseline {
		for _, id := range ids {
			known[p+"/"+id] = true
		}
	}
	var fresh []newestModel
	for _, m := range models {
		k := m.Provider + "/" + m.Name
		if known[k] || (w.Match != "" && !strings.Contains(m.Name, w.Match)) {
			continue
		}
		if _, ok := st.Seen[k]; ok {
			continue
		}
		known[k] = true
		fresh = append(fresh, m)
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].Created > fresh[j].Created })

	loc, lerr := s.Location()
	if lerr != nil || loc == nil {
		loc = time.UTC
	}
	today := now.In(loc).Format("2006-01-02")
	startedToday := 0
	for _, it := range st.Seen {
		if it.State == schedule.WatchStarted && it.ClaimedAt.In(loc).Format("2006-01-02") == today {
			startedToday++
		}
	}
	allow := w.PerDay() - startedToday

	var toStart []newestModel
	for _, m := range fresh {
		k := m.Provider + "/" + m.Name
		if len(toStart) < allow {
			toStart = append(toStart, m)
			st.Seen[k] = schedule.WatchItem{Provider: m.Provider, Model: m.Name, FirstSeen: now, State: schedule.WatchClaimed, ClaimedAt: now}
			continue
		}
		st.Seen[k] = schedule.WatchItem{Provider: m.Provider, Model: m.Name, FirstSeen: now, State: schedule.WatchSkipped, ClaimedAt: now,
			Reason: fmt.Sprintf("the watch had already started %d evaluations today; press Try again for this model on the Gateway page, then Run now on the schedule", startedToday+len(toStart))}
	}
	// Claim before spend: the claims are on disk before any launch.
	if err := schedule.SaveWatchState(root, st); err != nil {
		return nil, err
	}

	env := handlers.OrgEnv{Cfg: a.cfg, Mode: a.cfg.Server.Mode, Services: *svc}
	if env.Services.Projects == nil {
		if ps, err := projectfs.Open(root); err == nil {
			env.Services.Projects = ps
		}
	}
	res := &schedule.Result{}
	var lines []string
	for _, m := range toStart {
		k := m.Provider + "/" + m.Name
		it := st.Seen[k]
		spec2, reason := newModelSpec(spec, w, m)
		if reason == "" {
			if err := s.Approved.CheckCap(res.CostUSD + w.USDPerModel); err != nil {
				reason = "the spending approved for this check is used up; press Try again for this model on the Gateway page, then Run now on the schedule"
			}
		}
		if reason == "" {
			ev, err := launchScheduledEvaluation(ctx, env, spec2, "schedule:"+s.Name, s.Name)
			if err != nil {
				reason = err.Error()
			} else {
				it.State, it.EvaluationID = schedule.WatchStarted, ev.ID
				res.EvaluationID = ev.ID
				res.CostUSD += w.USDPerModel
				lines = append(lines, fmt.Sprintf("New model %s found; evaluation %s started with a %.2f USD budget", k, ev.ID, w.USDPerModel))
			}
		}
		if reason != "" {
			it.State, it.Reason = schedule.WatchFailed, reason
			lines = append(lines, fmt.Sprintf("New model %s found; not started: %s", k, reason))
		}
		st.Seen[k] = it
		if err := schedule.SaveWatchState(root, st); err != nil {
			return res, err
		}
	}
	if err := schedule.SaveWatchState(root, st); err != nil {
		return res, err
	}
	if len(lines) == 0 {
		res.Summary = fmt.Sprintf("No new model (checked %d models)", len(models))
	} else {
		res.Summary = strings.Join(lines, ". ")
	}
	return res, nil
}

// newModelSpec is the evaluation started for new model m: the setup named
// w.Current next to the same agent and version on m, under its own budget.
// The second value is why it cannot start, or "".
func newModelSpec(spec *schemas.EvaluationSpec, w *schedule.NewModelWatch, m newestModel) (schemas.EvaluationSpec, string) {
	out := *spec
	var cur *schemas.EvaluationArm
	for i := range spec.Arms {
		if spec.Arms[i].Label == w.Current {
			c := spec.Arms[i]
			cur = &c
			break
		}
	}
	if cur == nil {
		return out, fmt.Sprintf("the saved evaluation has no setup named %s", w.Current)
	}
	out.Arms = []schemas.EvaluationArm{*cur, {Label: m.Name + " (new)", Agent: cur.Agent, Version: cur.Version, Model: m.Name, Provider: m.Provider}}
	out.Mode = schemas.EvaluationModeModels
	out.BudgetUSD = w.USDPerModel
	out.Name = "New model check: " + m.Name
	return out, ""
}

// fetchNewestModels reads GET /api/models/newest of the org's gateway.
func fetchNewestModels(ctx context.Context, a *app, providers []string) ([]newestModel, error) {
	base := strings.TrimRight(a.cfg.Gateway.URL, "/")
	if base == "" {
		return nil, errors.New("no gateway is configured")
	}
	ctx, cancel := context.WithTimeout(ctx, watchFetchTimeout)
	defer cancel()
	q := url.Values{"providers": {strings.Join(providers, ",")}, "limit": {fmt.Sprint(watchNewestLimit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/models/newest?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	if env := a.cfg.Gateway.AdminTokenEnv; env != "" {
		if tok := os.Getenv(env); tok != "" {
			req.Header.Set("X-Bifrost-Setup-Token", tok)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bifrost answered %d", resp.StatusCode)
	}
	var out struct {
		Models []newestModel `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("Bifrost's answer did not parse: %w", err)
	}
	return out.Models, nil
}

// takeWatchLock creates the lock file p. busy is true when another check
// holds a lock younger than watchLockStale; an older one is taken over.
func takeWatchLock(p string) (unlock func(), busy bool, err error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(p) }, false, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, false, err
		}
		fi, serr := os.Stat(p)
		if serr == nil && time.Since(fi.ModTime()) < watchLockStale {
			return nil, true, nil
		}
		if rerr := os.Remove(p); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return nil, false, rerr
		}
	}
	return nil, true, nil
}
