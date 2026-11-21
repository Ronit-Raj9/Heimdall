package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Minimum sample before a capability score is ranked (N7): a score over
// fewer runs or tasks is shown with "not enough runs" and never ranked.
const (
	AgentBoardMinRuns  = 10
	AgentBoardMinTasks = 5
)

// AgentLeaderboard is GET /leaderboard/agents: the per-benchmark boards
// folded into one row per agent, version and model, with one score per
// report card capability (contract section 4, agent leaderboard). Only the
// org's real runs count: scripted (practice) runs never enter the
// per-benchmark boards, and practice benchmarks (the small made-up examples)
// are left out.
type AgentLeaderboard struct {
	Capabilities []string              `json:"capabilities"` // capabilities with at least one row, in display order
	Models       []string              `json:"models"`       // models with at least one row, sorted, for the filter
	Capability   string                `json:"capability"`   // the capability filter, empty for all
	Model        string                `json:"model"`        // the model filter, empty for all
	MinRuns      int                   `json:"min_runs"`     // runs a capability score needs to be ranked
	MinTasks     int                   `json:"min_tasks"`    // tasks a capability score needs to be ranked
	Rows         []AgentLeaderboardRow `json:"rows"`
	GeneratedAt  time.Time             `json:"generated_at"`
}

// AgentLeaderboardRow is one agent version on one model.
type AgentLeaderboardRow struct {
	Agent string `json:"agent"`
	// Version is the agent version the runs belong to, matched by agent
	// hash (the agent config without its model and provider); 0 when no
	// saved version matches.
	Version    int    `json:"version"`
	Model      string `json:"model"`
	Label      string `json:"label"`
	ConfigHash string `json:"config_hash"`
	AgentHash  string `json:"agent_hash,omitempty"`
	Baseline   bool   `json:"baseline"` // a built-in agent shown for comparison
	// Rank is the row's place by the filtered capability among rows with
	// enough runs; 0 without a capability filter or below the minimum.
	Rank         int                                   `json:"rank,omitempty"`
	Capabilities map[string]AgentLeaderboardCapability `json:"capabilities"`
	// CostUSD is the mean of the per-benchmark median cost per run,
	// weighted by runs; nil unless every run's cost is known (an unpriced
	// model is never $0). CostRuns counts the runs whose cost is known and
	// CostNote says why the others are not.
	CostUSD        *float64 `json:"cost_usd"`
	CostRuns       int      `json:"cost_runs"`
	CostNote       string   `json:"cost_note,omitempty"`
	Runs           int      `json:"runs"`
	LastEvaluation string   `json:"last_evaluation"`
	LastExperiment string   `json:"last_experiment"`
	// Customer is "simulated customer" on a row of runs whose customer a
	// pinned model played, with Simulator the pin; such a row is ranked on
	// its own, never pooled with scripted-user runs. Empty otherwise.
	Customer  string `json:"customer,omitempty"`
	Simulator string `json:"simulator,omitempty"`
}

// AgentLeaderboardCapability is one capability score of a row: pooled
// verified successes over graded runs on the capability's benchmarks, with
// the report card's 95% task-clustered bootstrap interval. Ranked is false
// below AgentBoardMinRuns runs or AgentBoardMinTasks tasks; Note then says
// so.
type AgentLeaderboardCapability struct {
	Score      float64  `json:"score"`
	CILow      float64  `json:"ci_low"`
	CIHigh     float64  `json:"ci_high"`
	Runs       int      `json:"runs"`
	Tasks      int      `json:"tasks"`
	Ranked     bool     `json:"ranked"`
	Note       string   `json:"note,omitempty"`
	Benchmarks []string `json:"benchmarks"`
}

// baselineAgents are the built-in agents marked baseline on the board.
var baselineAgents = map[string]bool{
	"codex": true, "claude-code": true, "mini-swe-agent": true, "opencode": true, "gemini-cli": true,
}

// PracticeBenchmark reports whether a benchmark id is one of the small
// made-up practice examples (the Acme examples and the coding-core
// starter; ui suiteDisplay.ts isPracticeSuite): never on the agent board.
func PracticeBenchmark(id string) bool {
	id, _, _ = strings.Cut(id, "@")
	return strings.HasPrefix(id, "acme-") || id == "coding-core"
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
	board, err := h.buildAgentBoard(work, quickOrgID(ctx, h.orgID), capFilter, modelFilter)
	if err != nil {
		sendDepError(ctx, h.mode, "agent leaderboard", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, board)
}

// rowAcc is a row under construction.
type rowAcc struct {
	row        AgentLeaderboardRow
	newest     time.Time
	caps       map[string]*capAcc
	costSum    float64
	costRuns   int  // runs of benchmarks whose cost is fully known
	costKnown  bool // every benchmark's cost known so far
	pricedRuns int  // runs whose cost is known, over every benchmark
	costNote   string
}

type capAcc struct {
	succ, n    int
	tasks      []reportcard.TaskTally
	benchmarks []string
}

// buildAgentBoard builds the clean board of each suite's newest version and
// folds the entries into rows.
func (h *LeaderboardHandler) buildAgentBoard(ctx context.Context, orgID, capFilter, modelFilter string) (*AgentLeaderboard, error) {
	var keys KeyLister
	if h.keys != nil {
		keys = h.keys
	}
	src := PricedLeaderboardSource(h.index, KeyPriceLookup(ctx, keys, orgID))
	now := h.now()
	first, err := leaderboard.Build(ctx, src, leaderboard.Query{Condition: schemas.ConditionClean, Now: now})
	if err != nil {
		return nil, err
	}
	versions := newVersionIndex(h.root, h.library)
	practice := h.practice
	if practice == nil {
		practice = PracticeBenchmark
	}
	rows := map[string]*rowAcc{}
	seen := map[string]bool{}
	for _, sv := range first.Suites {
		id, _, _ := strings.Cut(sv, "@")
		if seen[id] {
			continue // Suites is newest first: only the newest version counts
		}
		seen[id] = true
		if practice(id) {
			continue
		}
		lb, err := leaderboard.Build(ctx, src, leaderboard.Query{Suite: sv, Condition: schemas.ConditionClean, Now: now})
		if errors.Is(err, leaderboard.ErrBadQuery) {
			continue
		}
		if err != nil {
			return nil, err
		}
		capability := packs.CapabilityOf(id, h.suiteTrack(ctx, id))
		h.foldEntries(rows, versions, lb, id, capability, "")
		// Runs whose customer a model played rank on their own board per
		// simulator pin, as separate "simulated customer" rows.
		for _, pin := range leaderboard.SimulatorPins(src, sv) {
			sim, err := leaderboard.Build(ctx, src, leaderboard.Query{Suite: sv, Condition: schemas.ConditionClean, Now: now,
				Customer: leaderboard.CustomerSimulated, Simulator: pin})
			if errors.Is(err, leaderboard.ErrBadQuery) {
				continue
			}
			if err != nil {
				return nil, err
			}
			h.foldEntries(rows, versions, sim, id, capability, pin)
		}
	}
	return foldRows(rows, capFilter, modelFilter, now), nil
}

// addCost folds one benchmark entry's cost into the row. The entry's
// cost_usd is the median over the runs that observed it; its reason counts
// the runs that did not ("k of n runs unknown", "unknown in all n runs").
// Any such run makes the row's cost not known.
func (acc *rowAcc) addCost(e schemas.LeaderboardEntry) {
	c, ok := e.Metrics[leaderboard.MetricCostUSD]
	unknown := e.Runs
	if ok && c.State == schemas.ValueObserved && c.V != nil {
		unknown = 0
		var k, n int
		if _, err := fmt.Sscanf(c.Reason, "%d of %d runs unknown", &k, &n); err == nil && k > 0 {
			unknown = min(k, e.Runs)
		}
		if unknown == 0 {
			acc.costSum += *c.V * float64(e.Runs)
			acc.costRuns += e.Runs
		}
	} else if ok && c.State == schemas.ValueNotApplicable {
		unknown = 0
	}
	acc.pricedRuns += e.Runs - unknown
	if unknown > 0 {
		acc.costKnown = false
		if acc.costNote == "" && e.Model != "" {
			acc.costNote = "no price for model " + e.Model + ", or its usage was not recorded: set a price on its key in Models & keys"
		} else if acc.costNote == "" {
			acc.costNote = "usage or price not recorded"
		}
	}
}

// entryAgentHash is the agent hash (agentversions.AgentHash) of the
// manifest arm an entry's runs came from, read from its newest experiment;
// "" when the arm cannot be found.
func (h *LeaderboardHandler) entryAgentHash(e schemas.LeaderboardEntry) string {
	for _, exp := range e.Experiments {
		m, ok := h.index.Manifest(exp)
		if !ok || m == nil {
			continue
		}
		for _, arm := range m.Arms {
			if arm.ConfigHash != e.ConfigHash {
				continue
			}
			if ah, err := agentversions.AgentHash(arm.Config); err == nil {
				return strings.TrimPrefix(ah, "sha256:")
			}
		}
	}
	return ""
}

// capabilityNote says why a capability score is not ranked, "" when it is.
func capabilityNote(runs, tasks int) string {
	if runs >= AgentBoardMinRuns && tasks >= AgentBoardMinTasks {
		return ""
	}
	return fmt.Sprintf("not enough runs: %d runs over %d tasks; ranking needs %d runs over %d tasks",
		runs, tasks, AgentBoardMinRuns, AgentBoardMinTasks)
}

// foldEntries adds the entries of one suite's board to the rows. pin is the
// simulator pin of a simulated-customer board, "" for the default board; its
// rows are keyed apart so the two instruments never pool.
func (h *LeaderboardHandler) foldEntries(rows map[string]*rowAcc, versions *versionIndex, lb *schemas.Leaderboard, id, capability, pin string) {
	for _, e := range lb.Entries {
		if e.Runs == 0 || e.Synthetic {
			continue
		}
		agentHash := h.entryAgentHash(e)
		version := versions.lookup(e.Agent, agentHash)
		key := e.Agent + "\x00" + e.Model + "\x00"
		if pin != "" {
			key += "sim:" + pin + "\x00"
		}
		switch {
		case version > 0:
			key += "v" + strconv.Itoa(version)
		case agentHash != "":
			key += agentHash
		default:
			key += e.ConfigHash
		}
		acc := rows[key]
		if acc == nil {
			acc = &rowAcc{
				row: AgentLeaderboardRow{
					Agent: e.Agent, Version: version, Model: e.Model, Label: e.Label,
					ConfigHash: e.ConfigHash, AgentHash: agentHash, Baseline: baselineAgents[e.Agent],
				},
				caps:      map[string]*capAcc{},
				costKnown: true,
			}
			if pin != "" {
				acc.row.Customer, acc.row.Simulator = leaderboard.SimulatedCustomerLabel, pin
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
		for task, r := range e.TaskPasses {
			ca.tasks = append(ca.tasks, reportcard.TaskTally{Key: id + "\x00" + task, Succ: r.Num, Runs: r.Den})
		}
		ca.benchmarks = append(ca.benchmarks, id)
		acc.row.Runs += e.Runs
		acc.addCost(e)
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

// foldRows finishes the rows, applies the filters and orders them. With a
// capability filter, rows with enough runs on it rank by its score (Rank
// 1, 2, ...) and rows below the minimum follow, unranked, by more runs;
// without a filter there is no single score to rank by (capabilities come
// from different benchmarks and are never blended), so rows are listed by
// agent, then more runs, then version and model. Ties break on more runs,
// agent, version, model, hash.
func foldRows(rows map[string]*rowAcc, capFilter, modelFilter string, now time.Time) *AgentLeaderboard {
	out := &AgentLeaderboard{Capabilities: []string{}, Models: []string{}, Capability: capFilter, Model: modelFilter,
		MinRuns: AgentBoardMinRuns, MinTasks: AgentBoardMinTasks, Rows: []AgentLeaderboardRow{}, GeneratedAt: now}
	capsSeen, modelsSeen := map[string]bool{}, map[string]bool{}
	for _, acc := range rows {
		r := acc.row
		r.Capabilities = map[string]AgentLeaderboardCapability{}
		for c, ca := range acc.caps {
			if ca.n == 0 {
				continue
			}
			tasks := ca.tasks
			if len(tasks) == 0 { // no per-task tallies: one pooled cluster (exact interval over runs)
				tasks = []reportcard.TaskTally{{Key: c, Succ: ca.succ, Runs: ca.n}}
			}
			lo, hi := reportcard.TaskInterval(tasks)
			b := slices.Clone(ca.benchmarks)
			slices.Sort(b)
			nTasks := len(ca.tasks)
			note := capabilityNote(ca.n, nTasks)
			r.Capabilities[c] = AgentLeaderboardCapability{
				Score: float64(ca.succ) / float64(ca.n), CILow: lo, CIHigh: hi, Runs: ca.n, Tasks: nTasks,
				Ranked: note == "", Note: note, Benchmarks: slices.Compact(b),
			}
		}
		if len(r.Capabilities) == 0 {
			continue
		}
		r.CostRuns = acc.pricedRuns
		if acc.costKnown && acc.costRuns > 0 {
			v := acc.costSum / float64(acc.costRuns)
			r.CostUSD = &v
		} else if !acc.costKnown {
			r.CostNote = acc.costNote
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
			ca, cb := ra.Capabilities[capFilter], rb.Capabilities[capFilter]
			if ca.Ranked != cb.Ranked {
				if ca.Ranked {
					return -1
				}
				return 1
			}
			if ca.Ranked && ca.Score != cb.Score {
				if ca.Score > cb.Score {
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
	if capFilter != "" {
		rank := 0
		for i := range out.Rows {
			if out.Rows[i].Capabilities[capFilter].Ranked {
				rank++
				out.Rows[i].Rank = rank
			}
		}
	}
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

// versionIndex maps an agent's agent hash to its version
// (agentversions.VersionOf: the project's saved versions, else v1 of a
// built-in library agent), cached per agent and hash.
type versionIndex struct {
	root, library string
	cache         map[string]int
}

func newVersionIndex(root, library string) *versionIndex {
	return &versionIndex{root: root, library: library, cache: map[string]int{}}
}

// lookup returns the version whose agent hash matches, 0 when none does.
func (v *versionIndex) lookup(agent, agentHash string) int {
	if agent == "" || agentHash == "" || !agentversions.ValidID(agent) {
		return 0
	}
	k := agent + "\x00" + agentHash
	n, ok := v.cache[k]
	if !ok {
		n = agentversions.VersionOf(v.root, v.library, agent, agentHash)
		v.cache[k] = n
	}
	return n
}
