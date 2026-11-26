package modelswitch

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/modelid"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Run folder files the report reads.
const (
	fileMetrics = "metrics.json"
	fileLedger  = "proxy.ledger.jsonl"
)

// Per-run metrics the report reads.
const (
	metricCostUSD  = "cost_usd"
	metricWallTime = "wall_time_s"
)

// run is one graded clean run of one model.
type run struct {
	bench   string
	task    string
	success int
	cost    *float64 // nil when the run's cost is not known
	wall    *float64
	// latencies are the run's answered model call latencies (ms).
	latencies []float64
	calls     int // model calls in the gateway rows
	failures  int // calls the provider failed (5xx, 429, no status)
	fallbacks int
	// Served model counts of the run's model calls (W4 feature 8, from
	// gwfeature.ReadRows): known and the model asked for, known, unknown,
	// and the distinct served ids.
	servedAsAsked int
	servedKnown   int
	servedUnknown int
	servedModels  map[string]int
}

// benchRuns is one child experiment's clean graded runs by arm label.
type benchRuns struct {
	child    schemas.EvaluationChild
	manifest *schemas.Manifest
	arms     map[string][]run
	excluded map[string]map[string]int
	practice int
	// armOf maps experiment arm ids to evaluation labels.
	armOf map[string]string
}

// readBench reads one child's clean graded runs the way the report card
// counts them: per slot the first sealed run whose outcome is not
// aborted_infra and that no operator requeued, with an outcome that
// scores; practice arms (scripted model) are left out.
func readBench(ctx context.Context, src leaderboard.Source, arms []schemas.EvaluationArm, c schemas.EvaluationChild) (benchRuns, error) {
	br := benchRuns{child: c, arms: map[string][]run{}, excluded: map[string]map[string]int{}, armOf: map[string]string{}}
	if c.ExperimentID == "" {
		return br, nil
	}
	m, ok := src.Manifest(c.ExperimentID)
	if !ok || m == nil {
		return br, nil
	}
	if d, ok := src.LatestDecision(c.ExperimentID); ok && d == schemas.DecisionInvalid {
		return br, nil
	}
	br.manifest = m
	br.armOf = mapArms(arms, m)
	practice := practiceArms(src, m, c.ExperimentID)
	bySlot := map[string][]leaderboard.Run{}
	for _, r := range src.Runs(c.ExperimentID) {
		bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
	}
	slots := make([]string, 0, len(bySlot))
	for s := range bySlot {
		slots = append(slots, s)
	}
	slices.Sort(slots)
	read := fileReader(src)
	for _, s := range slots {
		if err := ctx.Err(); err != nil {
			return br, fmt.Errorf("modelswitch: %w", err)
		}
		runs := bySlot[s]
		slices.SortStableFunc(runs, func(x, y leaderboard.Run) int { return cmp.Compare(x.Run.Attempt, y.Run.Attempt) })
		i := slices.IndexFunc(runs, func(r leaderboard.Run) bool {
			return r.Sealed && r.Run.Outcome != schemas.OutcomeAbortedInfra && !r.Superseded
		})
		if i < 0 {
			continue
		}
		r := runs[i]
		label, ok := br.armOf[r.Slot.Arm]
		if !ok || cmp.Or(r.Slot.Condition, schemas.ConditionClean) != schemas.ConditionClean || r.Slot.StreamReplay {
			continue
		}
		if _, scores := r.Run.Outcome.Score(); !scores {
			if why := schemas.ExcludedReason(&r.Run); why != "" {
				if br.excluded[label] == nil {
					br.excluded[label] = map[string]int{}
				}
				br.excluded[label][why]++
			}
			continue
		}
		if practice[r.Slot.Arm] {
			br.practice++
			continue
		}
		metrics := readMetrics(src, r.Run.RunID)
		y, _ := leaderboard.VerifiedSuccess(r.Run.Outcome, metrics)
		g := run{bench: c.Benchmark, task: r.Slot.Task, success: int(y)}
		if v, ok := metrics[metricCostUSD].Float(); ok {
			g.cost = &v
		}
		if v, ok := metrics[metricWallTime].Float(); ok {
			g.wall = &v
		}
		sr := &schemas.SealedRun{Slot: r.Slot, Run: r.Run}
		if ev, err := costexplain.LoadRun(sr, g.success == 1, read); err == nil && ev != nil {
			for _, q := range ev.Requests {
				if q.Kind != "" && q.Kind != "message" {
					continue
				}
				g.calls++
				if q.FallbackIndex > 0 {
					g.fallbacks++
				}
				switch {
				case q.Status == "success":
					if q.LatencyMS > 0 {
						g.latencies = append(g.latencies, q.LatencyMS)
					}
				case q.Status == "error" && providerFailure(q.ErrorStatus):
					g.failures++
				}
			}
		}
		if rows, _, _, err := gwfeature.ReadRows(src, r.Run.RunID); err == nil {
			for _, q := range rows {
				if q.Kind != "" && q.Kind != "message" {
					continue
				}
				if q.ModelServed == "" {
					g.servedUnknown++
					continue
				}
				g.servedKnown++
				if modelid.SameModel(q.ModelAsked, q.ModelServed) {
					g.servedAsAsked++
				}
				if g.servedModels == nil {
					g.servedModels = map[string]int{}
				}
				g.servedModels[q.ModelServed]++
			}
		}
		br.arms[label] = append(br.arms[label], g)
	}
	return br, nil
}

// providerFailure says a failed model call counts against the provider's
// availability: a 5xx, a 429 or no status at all (timeout, connection).
// Any other 4xx is the agent's own malformed request.
func providerFailure(status int) bool {
	return status == 0 || status == 429 || status >= 500
}

// fileReader adapts the source's run files to costexplain's reader.
func fileReader(src leaderboard.Source) costexplain.RunFileReader {
	return func(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
		data, err := src.RunFile(sr.Run.RunID, name)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return data, true, nil
	}
}

// readMetrics reads a run's metrics.json; a missing or unreadable file is
// no metrics (the columns read unknown).
func readMetrics(src leaderboard.Source, runID string) map[string]schemas.Value {
	data, err := src.RunFile(runID, fileMetrics)
	if err != nil {
		return nil
	}
	var out map[string]schemas.Value
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// practiceArms marks the arms whose runs are practice data, by the
// leaderboard's rule: a smoke or scripted_upstream experiment, a scripted
// agent, or a sealed run labelled practice or whose proxy ledger shows the
// scripted model.
func practiceArms(src leaderboard.Source, m *schemas.Manifest, expID string) map[string]bool {
	out := map[string]bool{}
	for id, a := range m.Arms {
		out[id] = m.Experiment.NoPaidTraffic() || a.Config.Adapter == "scripted" || a.Config.Scripted != nil
	}
	for _, r := range src.Runs(expID) {
		if !r.Sealed || out[r.Slot.Arm] {
			continue
		}
		if r.Run.Practice {
			out[r.Slot.Arm] = true
			continue
		}
		data, err := src.RunFile(r.Run.RunID, fileLedger)
		if err != nil {
			continue
		}
		if p, _ := leaderboard.LedgerProvenance(data); p == leaderboard.ProvenanceScripted || p == leaderboard.ProvenanceMixed {
			out[r.Slot.Arm] = true
		}
	}
	return out
}

// mapArms maps experiment arm ids to evaluation labels: an arm id or
// manifest label equal to a label (any case), else the remaining arms in
// sorted id order matched to the remaining labels in order. The
// evaluation launcher names each experiment arm by its label.
func mapArms(arms []schemas.EvaluationArm, m *schemas.Manifest) map[string]string {
	out := map[string]string{}
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	used := map[string]bool{}
	for _, id := range ids {
		for _, a := range arms {
			if !used[a.Label] && (strings.EqualFold(id, a.Label) || strings.EqualFold(m.Arms[id].Label, a.Label)) {
				out[id], used[a.Label] = a.Label, true
				break
			}
		}
	}
	if len(out) == len(ids) {
		return out
	}
	var rest []string
	for _, a := range arms {
		if !used[a.Label] {
			rest = append(rest, a.Label)
		}
	}
	for _, id := range ids {
		if _, ok := out[id]; ok || len(rest) == 0 || id == "compat" {
			continue
		}
		out[id], rest = rest[0], rest[1:]
	}
	return out
}
