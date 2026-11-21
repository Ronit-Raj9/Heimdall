package handlers

// Why agents fail (lane X11): failure patterns per agent version.
//
// GET /agents/{id}/failure-patterns folds the triage of every experiment
// that ran the agent (BuildTriage: loops, unverified finishes, ignored
// errors, false completions) by agent version, so a builder sees which of
// their versions loops or claims done too early, with example runs.
// GET /evaluations/{id}/failure-patterns does the same over one
// evaluation's child experiments, grouped by the evaluation's arm labels,
// for the report card's failures section. Both are read only and reuse the
// deterministic detectors; they never say why a run failed (docs/triage.md).

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// failureExamplesMax is how many example runs each pattern carries.
const failureExamplesMax = 3

// failureExperimentsMax caps how many experiments one agent read folds,
// newest first, so a long history stays a bounded read.
const failureExperimentsMax = 200

// FailurePatterns is GET /agents/{id}/failure-patterns and GET
// /evaluations/{id}/failure-patterns.
type FailurePatterns struct {
	Agent      string                `json:"agent,omitempty"`
	Evaluation string                `json:"evaluation,omitempty"`
	Groups     []FailurePatternGroup `json:"groups"`
}

// FailurePatternGroup is one agent version (agent read) or one evaluation
// arm (evaluation read).
type FailurePatternGroup struct {
	// Key is "v<N>" for a saved version, "config:<hash prefix>" for a config
	// that is not a saved version, or the evaluation arm label.
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Agent      string   `json:"agent"`
	Version    int      `json:"version"` // 0 when the config is not a saved version
	ConfigHash string   `json:"config_hash,omitempty"`
	Models     []string `json:"models"`
	// Runs counts the counted, sealed runs read; Failed those that did not pass.
	Runs        int                   `json:"runs"`
	Failed      int                   `json:"failed"`
	Experiments []string              `json:"experiments"`
	LastAt      time.Time             `json:"last_at"`
	Patterns    []FailurePatternCount `json:"patterns"`
}

// FailurePatternCount is one pattern of a group: Count runs showed it out of
// Observed runs where it was measured; Unknown runs could not say.
type FailurePatternCount struct {
	Pattern     string           `json:"pattern"`
	Label       string           `json:"label"`
	Description string           `json:"description"`
	Count       int              `json:"count"`
	Observed    int              `json:"observed"`
	Unknown     int              `json:"unknown"`
	Examples    []FailureExample `json:"examples"`
}

// FailureExample is one run that showed a pattern, for a link to its page.
type FailureExample struct {
	RunID     string `json:"run_id"`
	ExpID     string `json:"exp_id"`
	PairID    string `json:"pair_id,omitempty"`
	Benchmark string `json:"benchmark"`
	Task      string `json:"task"`
	Outcome   string `json:"outcome"`
}

type failurePatternsHandler struct {
	env   OrgEnv
	evals ReportCardEvaluations
}

func init() {
	RegisterOrgArea("failurepatterns", registerFailurePatterns)
}

func registerFailurePatterns(env OrgEnv, r *router.Router) {
	var evals ReportCardEvaluations
	if env.Cfg != nil && env.Cfg.Root != "" {
		evals = NewFileEvaluations(env.Cfg.Root)
	}
	h := &failurePatternsHandler{env: env, evals: evals}
	r.GET(APIPrefix+"/agents/{id}/failure-patterns", env.R(h.getAgent))
	r.GET(APIPrefix+"/evaluations/{id}/failure-patterns", env.R(h.getEvaluation))
}

func (h *failurePatternsHandler) catalog() *schemas.Catalog {
	if h.env.Services.Catalog == nil {
		return nil
	}
	return h.env.Services.Catalog.Catalog()
}

func (h *failurePatternsHandler) root() string {
	if h.env.Cfg == nil {
		return ""
	}
	return h.env.Cfg.Root
}

// getAgent handles GET /api/bench/agents/{id}/failure-patterns: one group
// per version of the agent, newest run first.
func (h *failurePatternsHandler) getAgent(ctx *fasthttp.RequestCtx) {
	agent, ok := idParam(ctx, h.env.Mode, "id", "agent")
	if !ok {
		return
	}
	index := h.env.Services.Index
	if index == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, BuildAgentFailurePatterns(index, agent, newVersionIndex(h.root(), suite.LibraryDir()), h.catalog()))
}

// getEvaluation handles GET /api/bench/evaluations/{id}/failure-patterns:
// one group per evaluation arm.
func (h *failurePatternsHandler) getEvaluation(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "evaluation")
	if !ok {
		return
	}
	index := h.env.Services.Index
	switch {
	case index == nil:
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	case h.evals == nil:
		sendUnavailable(ctx, h.env.Mode, "evaluation store")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ev, err := h.evals.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, BuildEvaluationFailurePatterns(index, ev, h.catalog()))
}

// failureAcc folds experiments into one group.
type failureAcc struct {
	group    FailurePatternGroup
	patterns map[string]*FailurePatternCount
}

func newFailureAcc(g FailurePatternGroup, catalog *schemas.Catalog) *failureAcc {
	acc := &failureAcc{group: g, patterns: map[string]*FailurePatternCount{}}
	for _, name := range triagePatterns {
		label, desc := triageLabel(name, catalog)
		acc.patterns[name] = &FailurePatternCount{Pattern: name, Label: label, Description: desc, Examples: []FailureExample{}}
	}
	return acc
}

// add folds one experiment's arm into the group: run counts, pattern
// ratios and example runs (the newest experiment's examples come first
// because experiments are folded newest first).
func (a *failureAcc) add(index ReadIndex, m *schemas.Manifest, arm string, views []RunView, triage *schemas.Triage) {
	runs := 0
	for _, rv := range views {
		if rv.Slot.Arm != arm {
			continue
		}
		runs++
		if rv.Run.Outcome != schemas.OutcomePass {
			a.group.Failed++
		}
	}
	if runs == 0 {
		return
	}
	a.group.Runs += runs
	if !slices.Contains(a.group.Experiments, m.ExpID) {
		a.group.Experiments = append(a.group.Experiments, m.ExpID)
	}
	if m.CreatedAt.After(a.group.LastAt) {
		a.group.LastAt = m.CreatedAt
	}
	if model := m.Arms[arm].Config.Model; model != "" && !slices.Contains(a.group.Models, model) {
		a.group.Models = append(a.group.Models, model)
	}
	for _, g := range triage.Groups {
		p := a.patterns[g.Pattern]
		if p == nil {
			continue
		}
		r := g.PerArm[arm]
		p.Count += r.Num
		p.Observed += r.Den
		p.Unknown += r.Unknown
		for _, runID := range g.Runs[arm] {
			if len(p.Examples) >= failureExamplesMax {
				break
			}
			p.Examples = append(p.Examples, failureExample(index, m.ExpID, runID))
		}
	}
}

func (a *failureAcc) result() FailurePatternGroup {
	g := a.group
	g.Patterns = make([]FailurePatternCount, 0, len(triagePatterns))
	for _, name := range triagePatterns {
		g.Patterns = append(g.Patterns, *a.patterns[name])
	}
	slices.Sort(g.Models)
	if g.Models == nil {
		g.Models = []string{}
	}
	if g.Experiments == nil {
		g.Experiments = []string{}
	}
	return g
}

func failureExample(index ReadIndex, expID, runID string) FailureExample {
	ex := FailureExample{RunID: runID, ExpID: expID}
	if rv, ok := index.Run(runID); ok && rv != nil {
		ex.PairID, ex.Benchmark, ex.Task, ex.Outcome = rv.Slot.PairID, rv.Slot.Suite, rv.Slot.Task, string(rv.Run.Outcome)
	}
	return ex
}

// newestManifests returns the index's manifests newest first.
func newestManifests(index ReadIndex) []*schemas.Manifest {
	var out []*schemas.Manifest
	for _, id := range index.Experiments() {
		if m, ok := index.Manifest(id); ok && m != nil {
			out = append(out, m)
		}
	}
	slices.SortStableFunc(out, func(x, y *schemas.Manifest) int {
		return cmp.Or(y.CreatedAt.Compare(x.CreatedAt), cmp.Compare(x.ExpID, y.ExpID))
	})
	return out
}

// triageOf reads one experiment's triage and its counted sealed runs. An
// experiment whose metrics cannot be read is skipped (nil), so one broken
// run folder never hides the agent's other failures.
func triageOf(index ReadIndex, m *schemas.Manifest, catalog *schemas.Catalog) (*schemas.Triage, []RunView) {
	runs := index.Runs(m.ExpID)
	t, err := BuildTriage(index, m, runs, catalog)
	if err != nil {
		return nil, nil
	}
	return t, countedSealed(runs, armIDs(m))
}

// BuildAgentFailurePatterns folds every experiment arm that ran agent into
// one group per version (or per config hash when the config is not a saved
// version). Groups are ordered newest run first.
func BuildAgentFailurePatterns(index ReadIndex, agent string, versions *versionIndex, catalog *schemas.Catalog) *FailurePatterns {
	out := &FailurePatterns{Agent: agent, Groups: []FailurePatternGroup{}}
	accs := map[string]*failureAcc{}
	seen := 0
	for _, m := range newestManifests(index) {
		var arms []string
		for id, a := range m.Arms {
			if id != "compat" && a.Config.ID == agent {
				arms = append(arms, id)
			}
		}
		if len(arms) == 0 {
			continue
		}
		if seen++; seen > failureExperimentsMax {
			break
		}
		slices.Sort(arms)
		triage, views := triageOf(index, m, catalog)
		if triage == nil {
			continue
		}
		for _, arm := range arms {
			hash := m.Arms[arm].ConfigHash
			v := versions.lookup(agent, armAgentHash(m.Arms[arm].Config)) // versions match by agent hash
			key, label := fmt.Sprintf("v%d", v), fmt.Sprintf("v%d", v)
			if v == 0 {
				short := strings.TrimPrefix(hash, "sha256:")
				if len(short) > 12 {
					short = short[:12]
				}
				key, label = "config:"+short, "unsaved config "+short
			}
			acc := accs[key]
			if acc == nil {
				acc = newFailureAcc(FailurePatternGroup{Key: key, Label: label, Agent: agent, Version: v, ConfigHash: hash}, catalog)
				accs[key] = acc
			}
			acc.add(index, m, arm, views, triage)
		}
	}
	for _, acc := range accs {
		if acc.group.Runs > 0 {
			out.Groups = append(out.Groups, acc.result())
		}
	}
	slices.SortFunc(out.Groups, func(x, y FailurePatternGroup) int {
		return cmp.Or(y.LastAt.Compare(x.LastAt), cmp.Compare(y.Version, x.Version), cmp.Compare(x.Key, y.Key))
	})
	return out
}

// BuildEvaluationFailurePatterns folds an evaluation's child experiments
// into one group per evaluation arm, in the evaluation's arm order.
func BuildEvaluationFailurePatterns(index ReadIndex, ev *schemas.Evaluation, catalog *schemas.Catalog) *FailurePatterns {
	out := &FailurePatterns{Evaluation: ev.ID, Groups: []FailurePatternGroup{}}
	accs := map[string]*failureAcc{}
	for _, a := range ev.Spec.Arms {
		agent, v := reportcard.AgentRef(a.Agent)
		if a.Version != 0 {
			v = a.Version
		}
		g := FailurePatternGroup{Key: a.Label, Label: a.Label, Agent: agent, Version: v}
		accs[a.Label] = newFailureAcc(g, catalog)
	}
	for _, c := range ev.Children {
		if c.ExperimentID == "" {
			continue
		}
		m, ok := index.Manifest(c.ExperimentID)
		if !ok || m == nil {
			continue
		}
		triage, views := triageOf(index, m, catalog)
		if triage == nil {
			continue
		}
		labels := reportcard.MapArms(ev.Spec.Arms, m)
		ids := make([]string, 0, len(labels))
		for id := range labels {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if id == "compat" {
				continue
			}
			if acc := accs[labels[id]]; acc != nil {
				acc.add(index, m, id, views, triage)
				if acc.group.ConfigHash == "" {
					acc.group.ConfigHash = m.Arms[id].ConfigHash
				}
			}
		}
	}
	for _, a := range ev.Spec.Arms {
		g := accs[a.Label].result()
		if len(g.Models) == 0 && a.Model != "" {
			g.Models = []string{a.Model}
		}
		out.Groups = append(out.Groups, g)
	}
	return out
}
