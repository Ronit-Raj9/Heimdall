package main

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/forecast"
	"github.com/maximhq/bifrost/bench/schemas"
)

// agentRunsCacheMax bounds agentRunsCache; past it the cache empties itself.
const agentRunsCacheMax = 64

type agentRunsEntry struct {
	fp   string
	at   time.Time
	runs []forecast.HistoryRun
}

// agentRunsCache holds forecast.LoadAgentRuns per store root and agent set
// for historyCacheTTL, ended sooner by a new sealed run (storeFingerprint).
var agentRunsCache = struct {
	sync.Mutex
	m map[string]agentRunsEntry
}{m: map[string]agentRunsEntry{}}

// cachedAgentRuns is the org store's sealed runs of m's agents on any suite
// (forecast.Input.AgentRuns). Only the org's own store is read, never the
// operator library. A store that cannot be read gives no runs, so the rough
// estimate stays off and the old answer stands.
func (a *app) cachedAgentRuns(ctx context.Context, m *schemas.Manifest) []forecast.HistoryRun {
	if a.store == nil {
		return nil
	}
	root, fp := storeFingerprint(ctx, a.store)
	var key string
	if fp != "" {
		var agents []string
		for _, arm := range m.Arms {
			agents = append(agents, arm.Config.ID)
		}
		slices.Sort(agents)
		key = root + "\x00" + strings.Join(slices.Compact(agents), "\x00")
		agentRunsCache.Lock()
		e, ok := agentRunsCache.m[key]
		agentRunsCache.Unlock()
		if ok && e.fp == fp && time.Since(e.at) < historyCacheTTL {
			return e.runs
		}
	}
	h, err := forecast.LoadAgentRuns(ctx, a.store, m, sealedRunFile)
	if err != nil {
		a.logger.Warn("agent runs not read", "experiment", m.ExpID, "error", err.Error())
		return nil
	}
	if key != "" {
		agentRunsCache.Lock()
		if len(agentRunsCache.m) >= agentRunsCacheMax {
			clear(agentRunsCache.m)
		}
		agentRunsCache.m[key] = agentRunsEntry{fp: fp, at: time.Now(), runs: h.Runs}
		agentRunsCache.Unlock()
	}
	return h.Runs
}

// tokenPrice is the list price of one token of model from the price
// snapshot: low the input price, high the output price, mid their mean.
func (a *app) tokenPrice(model string) (float64, float64, float64, bool) {
	if a.prices == nil {
		return 0, 0, 0, false
	}
	p, ok := a.prices.Lookup(model)
	if !ok || p.InputPerMTok <= 0 || p.OutputPerMTok <= 0 {
		return 0, 0, 0, false
	}
	in, out := p.InputPerMTok/1e6, p.OutputPerMTok/1e6
	return min(in, out), (in + out) / 2, max(in, out), true
}
