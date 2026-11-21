package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/valyala/fasthttp"
)

// AgentLeaderboard is GET /leaderboard/agents: the per-benchmark boards
// folded into one row per agent, version and model, with one score per
// report card capability (contract section 4, agent leaderboard).
type AgentLeaderboard struct {
	Capabilities []string              `json:"capabilities"` // capabilities with at least one row, in display order
	Models       []string              `json:"models"`       // models with at least one row, sorted, for the filter
	Capability   string                `json:"capability"`   // the capability filter, empty for all
	Model        string                `json:"model"`        // the model filter, empty for all
	Rows         []AgentLeaderboardRow `json:"rows"`
	GeneratedAt  time.Time             `json:"generated_at"`
}

// AgentLeaderboardRow is one agent version on one model.
type AgentLeaderboardRow struct {
	Agent          string                                `json:"agent"`
	Version        int                                   `json:"version"` // 0 when the config hash is not in the agent's versions index
	Model          string                                `json:"model"`
	Label          string                                `json:"label"`
	ConfigHash     string                                `json:"config_hash"`
	Baseline       bool                                  `json:"baseline"` // a built-in agent shown for comparison
	Capabilities   map[string]AgentLeaderboardCapability `json:"capabilities"`
	CostUSD        *float64                              `json:"cost_usd"` // mean of the per-benchmark median cost per run, weighted by runs; nil when never observed
	Runs           int                                   `json:"runs"`
	LastEvaluation string                                `json:"last_evaluation"`
	LastExperiment string                                `json:"last_experiment"`
}

// AgentLeaderboardCapability is one capability score of a row: pooled
// verified successes over graded runs on the capability's benchmarks, with a
// 95% Clopper-Pearson interval.
type AgentLeaderboardCapability struct {
	Score      float64  `json:"score"`
	CILow      float64  `json:"ci_low"`
	CIHigh     float64  `json:"ci_high"`
	Runs       int      `json:"runs"`
	Benchmarks []string `json:"benchmarks"`
}

// baselineAgents are the built-in agents marked baseline on the board.
var baselineAgents = map[string]bool{
	"codex": true, "claude-code": true, "mini-swe-agent": true, "opencode": true, "gemini-cli": true,
}

// getAgentLeaderboard handles GET /leaderboard/agents?capability=&model=.
func (h *LeaderboardHandler) getAgentLeaderboard(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	capFilter := strings.TrimSpace(string(args.Peek("capability")))
	if capFilter != "" && !slices.Contains(packs.Capabilities(), capFilter) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"capability "+capFilter+" is not one of "+strings.Join(packs.Capabilities(), ", "))
		return
	}
	modelFilter := strings.TrimSpace(string(args.Peek("model")))
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	board, err := h.buildAgentBoard(work, capFilter, modelFilter)
	if err != nil {
		sendDepError(ctx, h.mode, "agent leaderboard", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, board)
}

// rowAcc is a row under construction.
type rowAcc struct {
	row      AgentLeaderboardRow
	newest   time.Time
	caps     map[string]*capAcc
	costSum  float64
	costRuns int
}

type capAcc struct {
	succ, n    int
	benchmarks []string
}

// buildAgentBoard builds the clean board of each suite's newest version and
// folds the entries into rows.
func (h *LeaderboardHandler) buildAgentBoard(ctx context.Context, capFilter, modelFilter string) (*AgentLeaderboard, error) {
	src := LeaderboardSource(h.index)
	now := h.now()
	first, err := leaderboard.Build(ctx, src, leaderboard.Query{Condition: schemas.ConditionClean, Now: now})
	if err != nil {
		return nil, err
	}
	versions := newVersionIndex(h.root)
	rows := map[string]*rowAcc{}
	seen := map[string]bool{}
	for _, sv := range first.Suites {
		id, _, _ := strings.Cut(sv, "@")
		if seen[id] {
			continue // Suites is newest first: only the newest version counts
		}
		seen[id] = true
		lb, err := leaderboard.Build(ctx, src, leaderboard.Query{Suite: sv, Condition: schemas.ConditionClean, Now: now})
		if errors.Is(err, leaderboard.ErrBadQuery) {
			continue
		}
		if err != nil {
			return nil, err
		}
		capability := packs.CapabilityOf(id, h.suiteTrack(ctx, id))
		for _, e := range lb.Entries {
			if e.Runs == 0 {
				continue
			}
			version := versions.lookup(e.Agent, e.ConfigHash)
			key := e.Agent + "\x00" + e.Model + "\x00"
			if version > 0 {
				key += "v" + strconv.Itoa(version)
			} else {
				key += e.ConfigHash
			}
			acc := rows[key]
			if acc == nil {
				acc = &rowAcc{
					row: AgentLeaderboardRow{
						Agent: e.Agent, Version: version, Model: e.Model, Label: e.Label,
						ConfigHash: e.ConfigHash, Baseline: baselineAgents[e.Agent],
					},
					caps: map[string]*capAcc{},
				}
				rows[key] = acc
			}
			ca := acc.caps[capability]
			if ca == nil {
				ca = &capAcc{}
				acc.caps[capability] = ca
			}
			ca.succ += e.Passes.Num
			ca.n += e.Passes.Den
			ca.benchmarks = append(ca.benchmarks, id)
			acc.row.Runs += e.Runs
			if c, ok := e.Metrics[leaderboard.MetricCostUSD]; ok && c.State == schemas.ValueObserved && c.V != nil {
				acc.costSum += *c.V * float64(e.Runs)
				acc.costRuns += e.Runs
			}
			if len(e.Experiments) > 0 {
				if m, ok := h.index.Manifest(e.Experiments[0]); ok && (acc.row.LastExperiment == "" || m.CreatedAt.After(acc.newest)) {
					acc.newest = m.CreatedAt
					acc.row.LastExperiment = e.Experiments[0]
					acc.row.Label = e.Label
				} else if acc.row.LastExperiment == "" {
					acc.row.LastExperiment = e.Experiments[0]
				}
			}
		}
	}
	return foldRows(rows, capFilter, modelFilter, now), nil
}

// foldRows finishes the rows, applies the filters and orders them. With a
// capability filter, rows rank by that capability's score; without one there
// is no single score to rank by (capabilities come from different benchmarks
// and are never blended), so rows are listed by agent, then more runs, then
// version and model. Ties break on more runs, agent, version, model, hash.
func foldRows(rows map[string]*rowAcc, capFilter, modelFilter string, now time.Time) *AgentLeaderboard {
	out := &AgentLeaderboard{Capabilities: []string{}, Models: []string{}, Capability: capFilter, Model: modelFilter, Rows: []AgentLeaderboardRow{}, GeneratedAt: now}
	capsSeen, modelsSeen := map[string]bool{}, map[string]bool{}
	for _, acc := range rows {
		r := acc.row
		r.Capabilities = map[string]AgentLeaderboardCapability{}
		for c, ca := range acc.caps {
			if ca.n == 0 {
				continue
			}
			lo, hi := stats.ClopperPearson(ca.succ, ca.n, 0.95)
			b := slices.Clone(ca.benchmarks)
			slices.Sort(b)
			r.Capabilities[c] = AgentLeaderboardCapability{
				Score: float64(ca.succ) / float64(ca.n), CILow: lo, CIHigh: hi, Runs: ca.n, Benchmarks: slices.Compact(b),
			}
		}
		if len(r.Capabilities) == 0 {
			continue
		}
		if acc.costRuns > 0 {
			v := acc.costSum / float64(acc.costRuns)
			r.CostUSD = &v
		}
		if r.Model != "" {
			modelsSeen[r.Model] = true
		}
		if modelFilter != "" && r.Model != modelFilter {
			continue
		}
		if capFilter != "" {
			if _, ok := r.Capabilities[capFilter]; !ok {
				continue
			}
		}
		for c := range r.Capabilities {
			capsSeen[c] = true
		}
		out.Rows = append(out.Rows, r)
	}
	slices.SortStableFunc(out.Rows, func(ra, rb AgentLeaderboardRow) int {
		if capFilter != "" {
			if ka, kb := ra.Capabilities[capFilter].Score, rb.Capabilities[capFilter].Score; ka != kb {
				if ka > kb {
					return -1
				}
				return 1
			}
		} else if ra.Agent != rb.Agent {
			return strings.Compare(ra.Agent, rb.Agent)
		}
		switch {
		case ra.Runs != rb.Runs:
			return rb.Runs - ra.Runs
		case ra.Agent != rb.Agent:
			return strings.Compare(ra.Agent, rb.Agent)
		case ra.Version != rb.Version:
			return ra.Version - rb.Version
		case ra.Model != rb.Model:
			return strings.Compare(ra.Model, rb.Model)
		}
		return strings.Compare(ra.ConfigHash, rb.ConfigHash)
	})
	for _, c := range packs.Capabilities() {
		if capsSeen[c] {
			out.Capabilities = append(out.Capabilities, c)
		}
	}
	for m := range modelsSeen {
		out.Models = append(out.Models, m)
	}
	slices.Sort(out.Models)
	return out
}

// suiteTrack is a suite's track from the suite source, empty without one or
// when the suite cannot be loaded (CapabilityOf then reads the id alone).
func (h *LeaderboardHandler) suiteTrack(ctx context.Context, id string) string {
	if h.suites == nil {
		return ""
	}
	s, err := h.suites.LoadSuite(ctx, id)
	if err != nil || s == nil {
		return ""
	}
	return s.Track
}

// versionIndex maps an agent's config hash to its saved version number from
// .heimdall/agent-versions/<id>/versions.json (contract section 2), read
// lazily once per agent.
type versionIndex struct {
	root   string
	byAgnt map[string]map[string]int
}

func newVersionIndex(root string) *versionIndex {
	return &versionIndex{root: root, byAgnt: map[string]map[string]int{}}
}

// lookup returns the version whose hash matches, 0 when none does or there
// is no index. A hash with and without its "sha256:" prefix match.
func (v *versionIndex) lookup(agent, hash string) int {
	if v.root == "" || agent == "" || hash == "" || strings.ContainsAny(agent, `/\`) || agent == ".." {
		return 0
	}
	m, ok := v.byAgnt[agent]
	if !ok {
		m = map[string]int{}
		v.byAgnt[agent] = m
		raw, err := os.ReadFile(filepath.Join(v.root, ".heimdall", "agent-versions", agent, "versions.json"))
		if err == nil {
			var list []struct {
				Version int    `json:"version"`
				Hash    string `json:"hash"`
			}
			if json.Unmarshal(raw, &list) == nil {
				for _, e := range list {
					m[strings.TrimPrefix(e.Hash, "sha256:")] = e.Version
				}
			}
		}
	}
	return m[strings.TrimPrefix(hash, "sha256:")]
}
