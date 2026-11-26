package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/bifrostfeatures"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Semantic cache correctness check (audit/W4_DESIGN_3_BIFROST.md section 8):
// one cached setup at the lowest threshold of a grid; every hit row records
// its similarity, so the hits, wrong answers and savings of each higher
// threshold are read from the same runs.

func init() {
	Register(Feature{ID: schemas.GatewayFeatureSemanticCache, Name: "Semantic cache", Draft: draftCache, Compute: computeCache})
}

const (
	cacheDefaultGrid   = "0.80,0.85,0.90,0.95,0.98"
	cacheDefaultTarget = 0.02
	cacheMinDecided    = 10
)

type cacheDetails struct {
	Thresholds     []cacheThreshold `json:"thresholds"`
	Recommended    *float64         `json:"recommended,omitempty"`
	SameQuestion   int              `json:"same_question_hits"`
	Undecided      int              `json:"undecided_hits"`
	UnknownSim     int              `json:"unknown_similarity_hits"`
	SingleCallTask int              `json:"single_call_tasks"`
	Estimated      bool             `json:"estimated"` // always true: built from one run at the lowest threshold
}

type cacheThreshold struct {
	Threshold  float64  `json:"threshold"`
	Hits       int      `json:"hits"`
	Decided    int      `json:"decided"`
	Wrong      int      `json:"wrong"`
	WrongShare *float64 `json:"wrong_share,omitempty"`
	WrongHigh  *float64 `json:"wrong_high,omitempty"`
	SavedUSD   float64  `json:"saved_usd"`
	SavedShare *float64 `json:"saved_share,omitempty"`
	HitShare   *float64 `json:"hit_share,omitempty"`
}

// cacheGrid parses the grid option: 2 to 8 distinct thresholds in (0, 1),
// returned in ascending order.
func cacheGrid(opts map[string]string) ([]float64, error) {
	s := strings.TrimSpace(opts["grid"])
	if s == "" {
		s = cacheDefaultGrid
	}
	var out []float64
	for _, part := range strings.Split(s, ",") {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || !(v > 0 && v < 1) {
			return nil, errors.New("Each threshold must be a number above 0 and below 1, for example 0.80,0.90,0.95.")
		}
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	if len(out) < 2 || len(out) > 8 {
		return nil, errors.New("Give between 2 and 8 thresholds.")
	}
	slices.Sort(out)
	return out, nil
}

func cacheTarget(opts map[string]string) (float64, error) {
	s := strings.TrimSpace(opts["target"])
	if s == "" {
		return cacheDefaultTarget, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v > 0 && v < 1) {
		return 0, errors.New("The largest share of wrong answers must be a number above 0 and below 1.")
	}
	return v, nil
}

func draftCache(ctx context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, nil, errors.New("Pick the model both setups use.")
	}
	grid, err := cacheGrid(req.Options)
	if err != nil {
		return nil, nil, err
	}
	if _, err := cacheTarget(req.Options); err != nil {
		return nil, nil, err
	}
	ttl := strings.TrimSpace(req.Options["ttl"])
	if ttl == "" {
		ttl = "1h"
	}
	notLoaded := errors.New("Bifrost's semantic cache is not loaded on the gateway these runs use. Load it with a vector store and an embedding provider first.")
	if env.GatewayGet == nil {
		return nil, nil, notLoaded
	}
	var loaded struct {
		Plugins []string `json:"plugins"`
	}
	if err := env.GatewayGet(ctx, "/api/plugins/loaded", &loaded); err != nil || !slices.Contains(loaded.Plugins, "semantic_cache") {
		return nil, nil, notLoaded
	}
	low := grid[0]
	arms := map[string]schemas.ArmSpec{
		"A": {Label: "cache off", Set: map[string]any{"model": model}},
		"B": {Label: fmt.Sprintf("cache on at %.2f", low), Set: map[string]any{
			"model":           model,
			"gateway.bifrost": map[string]any{"semantic_cache": map[string]any{"mode": schemas.SemanticCacheSemantic, "scope": schemas.SemanticCacheScopeArm, "ttl": ttl, "threshold": low}},
		}},
	}
	exp := p3Experiment(env, req, schemas.GatewayFeatureSemanticCache, "A", []string{"B"}, arms)
	notes := []string{"Answers are judged by your benchmark's own grader, so only tasks the agent answers in one model call count for correctness."}
	return exp, notes, nil
}

func messageRows(r Run) []Row {
	var out []Row
	for _, row := range r.Rows {
		if row.Kind == "message" {
			out = append(out, row)
		}
	}
	return out
}

func computeCache(ctx context.Context, in Input) Effect {
	a, b := in.Test.Control, "B"
	if len(in.Test.Variants) > 0 {
		b = in.Test.Variants[0]
	}
	grid, err := cacheGrid(in.Test.Options)
	if err != nil {
		grid, _ = cacheGrid(nil)
	}
	target, err := cacheTarget(in.Test.Options)
	if err != nil {
		target = cacheDefaultTarget
	}
	armTh := grid[0]
	e := Effect{Variant: b, Arms: ArmResults(in.Runs, in.Manifest, []string{a, b})}
	det := cacheDetails{Estimated: true, Thresholds: []cacheThreshold{}}
	finish := func() Effect {
		raw, _ := json.Marshal(det)
		e.Details = raw
		if det.UnknownSim > 0 && (e.State == StateMeasured || e.State == StateNotEnoughData) {
			e.Headline = strings.TrimSpace(fmt.Sprintf("%s %d cache hits have no recorded similarity (Bifrost logs none for exact repeats), so they are left out of the threshold counts and not counted as wrong.", e.Headline, det.UnknownSim))
		}
		e.Result = p3Result(e, 0.03)
		return e
	}

	var bRuns []Run
	stored, misses := false, map[string]Row{}
	for _, r := range in.Runs {
		if r.Arm != b {
			continue
		}
		bRuns = append(bRuns, r)
		for _, row := range r.Rows {
			if row.CacheID != "" {
				stored = true
				if !row.CacheHit {
					if _, seen := misses[row.CacheID]; !seen {
						misses[row.CacheID] = row
					}
				}
			}
		}
	}
	slices.SortStableFunc(bRuns, func(x, y Run) int { return strings.Compare(x.RunID, y.RunID) })
	if !stored {
		e.State = StateNotApplied
		e.Reason = "Bifrost stored nothing for the cache setup: the agent did not send the cache headers, or the plugin skipped every request (conversations longer than its conversation_history_threshold are never cached)"
		e.Headline = e.Reason + "."
		return finish()
	}
	for _, r := range bRuns {
		for _, row := range r.Rows {
			if row.CacheHit && row.CacheThreshold != nil && math.Abs(*row.CacheThreshold-armTh) > 1e-6 {
				e.State = StateNotApplied
				e.Reason = fmt.Sprintf("Bifrost used threshold %.2f, not the setup's %.2f", *row.CacheThreshold, armTh)
				e.Headline = e.Reason + "."
				return finish()
			}
		}
	}

	// Single call tasks, and which tasks A passed at least once.
	aRuns, aPassed := map[string]int{}, map[string]bool{}
	single := map[string]bool{}
	for _, r := range in.Runs {
		if r.Arm != a {
			continue
		}
		n := len(messageRows(r))
		if aRuns[r.Task] == 0 {
			single[r.Task] = true
		}
		aRuns[r.Task]++
		if n != 1 {
			single[r.Task] = false
		}
		if r.Success != nil && *r.Success {
			aPassed[r.Task] = true
		}
	}
	for _, s := range single {
		if s {
			det.SingleCallTask++
		}
	}

	// Origin of each hit.
	rr := make([]bifrostfeatures.RunRows, 0, len(bRuns))
	for _, r := range bRuns {
		x := bifrostfeatures.RunRows{RunID: r.RunID, Task: r.Task, Arm: r.Arm, Condition: r.Condition, Trial: r.Trial}
		for _, row := range r.Rows {
			x.Rows = append(x.Rows, schemas.GatewayRequest{ID: row.ID, CacheHit: row.CacheHit, CacheID: row.CacheID})
		}
		rr = append(rr, x)
	}
	scope := map[string]string{}
	for _, rp := range bifrostfeatures.CacheReplays(rr).Replays {
		scope[rp.HitRun+"\x00"+rp.Row] = rp.Scope
	}

	type hit struct {
		sim            float64
		decided, wrong bool
		saved          float64
	}
	var hits []hit
	msgRows, bCost := 0, 0.0
	for _, r := range bRuns {
		if r.CostUSD != nil {
			bCost += *r.CostUSD
		}
		for _, row := range messageRows(r) {
			msgRows++
			if !row.CacheHit {
				continue
			}
			if !row.RawKnown || row.CacheSimilarity == nil {
				det.UnknownSim++
				continue
			}
			h := hit{sim: *row.CacheSimilarity}
			if m, ok := misses[row.CacheID]; ok && m.CostUSD != nil {
				h.saved = *m.CostUSD
			}
			switch scope[r.RunID+"\x00"+row.ID] {
			case "same_run", "same_task":
				det.SameQuestion++
			case "cross_task":
				switch {
				case !single[r.Task] || r.Success == nil:
					det.Undecided++
				case *r.Success:
					h.decided = true
				case aPassed[r.Task]:
					h.decided, h.wrong = true, true
				default:
					det.Undecided++
				}
			default:
				det.Undecided++
			}
			hits = append(hits, h)
		}
	}

	for _, t := range grid {
		th := cacheThreshold{Threshold: t}
		for _, h := range hits {
			if h.sim < t {
				continue
			}
			th.Hits++
			th.SavedUSD += h.saved
			if h.decided {
				th.Decided++
				if h.wrong {
					th.Wrong++
				}
			}
		}
		if th.Decided > 0 {
			th.WrongShare = p3Ptr(float64(th.Wrong) / float64(th.Decided))
			_, hi := stats.ClopperPearson(th.Wrong, th.Decided, 0.95)
			th.WrongHigh = &hi
		}
		if bCost+th.SavedUSD > 0 {
			th.SavedShare = p3Ptr(th.SavedUSD / (bCost + th.SavedUSD))
		}
		if msgRows > 0 {
			th.HitShare = p3Ptr(float64(th.Hits) / float64(msgRows))
		}
		det.Thresholds = append(det.Thresholds, th)
	}

	solve := PairedTasks(in.Runs, a, b, SolveValue)
	cost := PairedTasks(in.Runs, a, b, CostValue)
	lat := PairedTasks(in.Runs, a, b, LatencyValue)
	e.Tasks = solve.Tasks
	e.SolveDelta, e.CostDelta, e.LatencyDelta = pairedInterval(solve), pairedInterval(cost), pairedInterval(lat)

	totalHits := len(hits) + det.UnknownSim
	if totalHits == 0 {
		e.State = StateNotEnoughData
		e.Reason = "no cache hits. If Bifrost keeps cache entries per virtual key (identity scoping), runs never share answers; set disable_identity_scoping on the plugin to measure sharing."
		e.Headline = "No cache hits yet."
		return finish()
	}
	maxDecided := 0
	for _, th := range det.Thresholds {
		maxDecided = max(maxDecided, th.Decided)
	}
	if maxDecided < cacheMinDecided {
		e.State = StateNotEnoughData
		e.Reason = fmt.Sprintf("%d cache hits could be judged; %d are needed at a threshold", maxDecided, cacheMinDecided)
		e.Headline = "Not enough data yet: " + e.Reason + "."
		return finish()
	}
	e.State = StateMeasured
	for _, th := range det.Thresholds {
		if th.Decided >= cacheMinDecided && th.WrongHigh != nil && *th.WrongHigh <= target {
			t := th.Threshold
			det.Recommended = &t
			e.Headline = fmt.Sprintf("Set the threshold to %.2f: %d wrong in %d judged hits (up to %.0f%%).", t, th.Wrong, th.Decided, *th.WrongHigh*100)
			body, _ := json.MarshalIndent(map[string]any{"name": "semantic_cache", "config": map[string]any{"threshold": t}}, "", "  ")
			e.Setting = &BifrostSetting{
				Kind:  "plugin_config",
				Title: fmt.Sprintf("Semantic cache threshold %.2f", t),
				Where: "Bifrost dashboard, Plugins, semantic cache; or plugins[].config in config.json.",
				Body:  string(body),
				Note:  "Keep your other semantic_cache settings; change only threshold. Requests also need a cache key (x-bf-cache-key) or the plugin's default_cache_key.",
			}
			return finish()
		}
	}
	e.Headline = "Keep the cache to exact repeats: every threshold served wrong answers."
	anyWrong := false
	for _, th := range det.Thresholds {
		anyWrong = anyWrong || th.Wrong > 0
	}
	if !anyWrong {
		e.Headline = fmt.Sprintf("Keep the cache to exact repeats: no threshold has enough judged hits yet to show its share of wrong answers is at most %.0f%%.", target*100)
	}
	return finish()
}
