// Package reportcard builds the report card of an evaluation (agent-first
// contract section 4): one score per benchmark and arm with its interval,
// cost and effort columns, a rollup per capability, how each arm holds up
// under each fault, safety rates, and the A/B verdicts from verdictab.
//
// It is pure compute over the run folder's read side: it reads each child
// experiment's manifest, runs and metrics.json through leaderboard.Source,
// the same way the leaderboard does, and writes nothing.
package reportcard

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/learning"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/verdictab"
)

// Run-level metric ids read from metrics.json (metrics/builtin.go).
const (
	metricCostUSD        = "cost_usd"
	metricTokens         = "tokens"
	metricSteps          = "steps"
	metricToolCalls      = "tool_calls"
	metricWallTime       = "wall_time_s"
	metricSafeSuccess    = "safe_success"
	metricCheatingRate   = "cheating_rate"
	metricAttackSucceded = "reward_attack_succeeded" // AgentDojo reward dimension under an attack
	// Report-only process metrics (graders/process.go).
	metricProcessMatch     = "process_tool_match"
	metricProcessRatio     = "process_step_ratio"
	metricProcessRedundant = "process_redundant_calls"

	// Persistence metrics (metrics/: process kill and resume, budget squeeze).
	metricResumedWithoutRedo = "resumed_without_redo"
	metricGracefulFinish     = "graceful_finish"
	metricCostNormalized     = "cost_normalized_success"
)

// Conf is the level of the score interval.
const Conf = 0.95

// Faults an evaluation can ask for besides clean.
const (
	FaultModelDown   = "model-down"
	FaultBrokenTool  = "broken-tool"
	FaultMisleading  = "misleading-instruction"
	fileMetrics      = "metrics.json"
	fileLedger       = "proxy.ledger.jsonl"
	capabilityOther  = "other"
	capabilitySafety = "safety"
)

// Arm is one arm of the report.
type Arm struct {
	Label   string `json:"label"`
	Agent   string `json:"agent"`
	Version int    `json:"version,omitempty"` // the version that ran; 0 for an evaluation from before versions were recorded
	// LatestAtLaunch: the evaluation asked for the latest version and
	// Version is the one that was latest when it was created.
	LatestAtLaunch bool   `json:"latest_at_launch,omitempty"`
	Model          string `json:"model"`
}

// Metrics is one arm's numbers on one benchmark, capability or condition.
// Score is the verified success rate over graded runs with its
// task-clustered bootstrap interval. CostUSD and Tokens are sums over the
// runs, nil when any run's value is not known (an unpriced model, a run
// whose usage was not recorded): a partial sum would read as the full cost,
// so CostRuns and TokenRuns count the runs that were known and CostNote says
// why the rest were not. Steps, ToolCalls and WallS are medians per run, as
// the leaderboard shows them (a capability rollup weights each benchmark's
// median by its runs).
type Metrics struct {
	Score     float64  `json:"score"`
	CILow     float64  `json:"ci_low"`
	CIHigh    float64  `json:"ci_high"`
	Runs      int      `json:"runs"`
	Tasks     int      `json:"tasks"`
	CostUSD   *float64 `json:"cost_usd"`
	CostRuns  int      `json:"cost_runs"`           // runs whose cost is known
	CostNote  string   `json:"cost_note,omitempty"` // why the other runs' cost is not known
	Tokens    *float64 `json:"tokens"`
	TokenRuns int      `json:"token_runs"` // runs whose token count is known
	Steps     float64  `json:"steps"`
	ToolCalls float64  `json:"tool_calls"`
	WallS     float64  `json:"wall_s"`
	// Excluded counts the runs left out of Score by reason: out_of_memory
	// (the out-of-memory killer ended the agent), infra_error,
	// invalid_evaluator, unknown. They are never the agent's failures.
	Excluded map[string]int `json:"excluded,omitempty"`
	// Process holds the report-only process scores of the runs whose task
	// names a reference tool path; nil when none does. It never feeds Score.
	Process *Process `json:"process,omitempty"`
}

// Process is one arm's process scores: ToolMatch is the share of runs whose
// tool path matched the reference, StepRatio the median of tool calls over
// the reference's, RedundantCalls the median of repeated identical calls.
// Runs counts the runs with a tool path score; a field no run observed is
// null.
type Process struct {
	Runs           int      `json:"runs"`
	ToolMatch      *float64 `json:"tool_match"`
	StepRatio      *float64 `json:"step_ratio"`
	RedundantCalls *float64 `json:"redundant_calls"`
}

// Capability is the rollup of every benchmark of one capability.
type Capability struct {
	Capability string             `json:"capability"`
	Benchmarks []string           `json:"benchmarks"`
	Arms       map[string]Metrics `json:"arms"`
}

// Benchmark is one benchmark's row.
type Benchmark struct {
	Benchmark    string                      `json:"benchmark"`
	Capability   string                      `json:"capability"`
	ExperimentID string                      `json:"experiment_id"`
	Arms         map[string]Metrics          `json:"arms"`
	Verdict      *verdictab.BenchmarkVerdict `json:"verdict,omitempty"`
	// ReliabilityCard is each arm's repeatability on the clean runs; an arm
	// with no clean runs has no entry.
	ReliabilityCard map[string]*ReliabilityCard `json:"reliability_card"`
}

// FaultScore is one arm's score under a fault and its change from clean on
// the same benchmarks (negative: the fault hurt).
type FaultScore struct {
	Score float64 `json:"score"`
	Drop  float64 `json:"drop"`
	Runs  int     `json:"runs"`
}

// Reliability is one fault condition.
type Reliability struct {
	Fault      string                `json:"fault"`
	Benchmarks []string              `json:"benchmarks"`
	Arms       map[string]FaultScore `json:"arms"`
}

// Safety holds the safety rates by arm label. AttackResisted is the share of
// attacked AgentDojo runs where the attack did not succeed; CheatRate is the
// share of impossible-task runs whose rigged tests passed.
type Safety struct {
	AttackResisted map[string]float64 `json:"attack_resisted"`
	CheatRate      map[string]float64 `json:"cheat_rate"`
}

// Report is GET /evaluations/{id}/report.
type Report struct {
	Evaluation   string                    `json:"evaluation"`
	Status       string                    `json:"status,omitempty"`
	Split        string                    `json:"split"`          // public, or held_out for a held-out evaluation
	Mode         string                    `json:"mode,omitempty"` // the evaluation's mode; "score" has one arm and no verdict
	Arms         []Arm                     `json:"arms"`
	Capabilities []Capability              `json:"capabilities"`
	Benchmarks   []Benchmark               `json:"benchmarks"`
	Reliability  []Reliability             `json:"reliability"`
	Resilience   []Resilience              `json:"resilience"`
	Safety       Safety                    `json:"safety"`
	Alignment    *Alignment                `json:"alignment,omitempty"` // Safety and alignment section (alignment.go)
	Overall      *verdictab.OverallVerdict `json:"overall,omitempty"`
	// PracticeRuns counts the runs left out because the scripted practice
	// model answered them (X01); Note explains it when it is not zero.
	PracticeRuns int               `json:"practice_runs,omitempty"`
	Note         string            `json:"note,omitempty"`
	Totals       map[string]Totals `json:"totals,omitempty"` // per arm label, over every benchmark
	// Learning is the learning-over-time summary of every benchmark run as a
	// stream (experiment stream:), per arm; nil when none was.
	Learning []LearningRow `json:"learning,omitempty"`
	// Persistence is how each arm resumed after a kill, finished under a
	// squeezed budget and converted spend into success; nil when no run
	// reported any of it.
	Persistence []PersistenceRow `json:"persistence,omitempty"`
	// Voice is each arm's call quality on the voice benchmarks (voice.go);
	// nil when no run was a voice call.
	Voice []VoiceRow `json:"voice,omitempty"`
}

// LearningRow is one arm's learning summary on one benchmark.
type LearningRow struct {
	Benchmark string `json:"benchmark"`
	Arm       string `json:"arm"`
	// Control names the no-memory control forward transfer is measured
	// against ("codex v2 on m (no memory)"); "" when the stream had none.
	Control string `json:"control,omitempty"`
	learning.Summary
}

// Rate is a mean over the runs that observed a metric.
type Rate struct {
	Value float64 `json:"value"`
	Runs  int     `json:"runs"`
}

// PersistenceRow is one arm's persistence numbers. A nil field had no run
// that observed it.
type PersistenceRow struct {
	Arm                   string `json:"arm"`
	ResumedWithoutRedo    *Rate  `json:"resumed_without_redo,omitempty"`
	GracefulFinish        *Rate  `json:"graceful_finish,omitempty"`
	CostNormalizedSuccess *Rate  `json:"cost_normalized_success,omitempty"`
}

// Options tune Build. Zero values take the defaults.
type Options struct {
	// CapabilityOf names the capability of a benchmark (suite id). nil uses
	// DefaultCapability.
	CapabilityOf func(benchmark string) string
	// FaultOf maps a benchmark's experiment condition id to its evaluation
	// fault (model-down, broken-tool, misleading-instruction), Attack for an
	// attack probe, "" for clean or anything else. It is read from the
	// benchmark's fault and probe metadata, never guessed from the id. A
	// child's own FaultOf, fixed at launch, wins over it. nil uses
	// DefaultFault.
	FaultOf func(benchmark, condition string) string
	// Arms keeps only these arm labels; empty keeps every arm.
	Arms []string
}

// graded is one counted, sealed, scored run.
type graded struct {
	task        string
	condition   string
	success     int
	metrics     map[string]schemas.Value
	gates       map[string]bool // gate name -> fired, for the gates that ran (alignment.go)
	trial       int
	streamIndex int  // stream mode: 1-based place in the chain; 0 outside a stream
	replay      bool // stream mode: a replay run, kept out of every score
}

// childRuns is one child experiment's graded runs by arm label, and the
// clean runs excluded from the score by arm label and reason. Replay runs
// of a stream are kept apart in replays so no score counts them; fresh are
// the runs of the stream's fresh-memory arm.
type childRuns struct {
	child    schemas.EvaluationChild
	arms     map[string][]graded
	practice int // runs left out as practice (scripted model)
	excluded map[string]map[string]int
	replays  map[string][]graded
	fresh    []graded
	stream   *schemas.StreamSpec
	freshArm string // label of the fresh arm, "" when it has none
	// freshName is the fresh arm's display name ("codex v2 on m (no
	// memory)"), "" without one.
	freshName string
}

// Build computes the report of ev over src. A child with no experiment or
// whose manifest the source does not know adds a benchmark row with no runs.
func Build(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation, opt Options) (*Report, error) {
	if ev == nil {
		return nil, errors.New("reportcard: evaluation is nil")
	}
	capOf := opt.CapabilityOf
	if capOf == nil {
		capOf = DefaultCapability
	}
	fallback := opt.FaultOf
	if fallback == nil {
		fallback = func(_, c string) string { return DefaultFault(c) }
	}
	faultOf := func(cr childRuns, condition string) string {
		if condition == schemas.ConditionClean {
			return ""
		}
		if cr.child.FaultOf != nil {
			f := cr.child.FaultOf[condition]
			if f == schemas.ConditionClean {
				return ""
			}
			return f
		}
		return fallback(cr.child.Benchmark, condition)
	}
	split := ev.Spec.Split
	if split == "" {
		split = schemas.SplitPublic
	}
	rep := &Report{
		Evaluation: ev.ID, Status: ev.Status, Split: split, Mode: ev.Spec.Mode, Arms: []Arm{}, Capabilities: []Capability{}, Benchmarks: []Benchmark{},
		Reliability: []Reliability{}, Resilience: []Resilience{}, Safety: Safety{AttackResisted: map[string]float64{}, CheatRate: map[string]float64{}},
	}
	// Score mode is one arm scored on its own. Evaluations launched before
	// one-arm experiments existed also ran a repeat of that arm; the report
	// shows only the arm asked for and never an A/B verdict.
	specArms := ev.Spec.Arms
	score := ev.Spec.Mode == schemas.EvaluationModeScore
	if score && len(specArms) > 1 {
		specArms = specArms[:1]
	}
	var labels []string
	for _, a := range specArms {
		if len(opt.Arms) > 0 && !slices.Contains(opt.Arms, a.Label) {
			continue
		}
		id, v := AgentRef(a.Agent)
		if a.Version != 0 {
			v = a.Version
		}
		rep.Arms = append(rep.Arms, Arm{Label: a.Label, Agent: id, Version: v, LatestAtLaunch: a.LatestAtLaunch && a.Version > 0, Model: a.Model})
		labels = append(labels, a.Label)
	}

	var children []childRuns
	for _, c := range ev.Children {
		if c.Skipped != "" {
			continue
		}
		cr, err := readChild(ctx, src, specArms, c)
		if err != nil {
			return nil, err
		}
		children = append(children, cr)
		rep.PracticeRuns += cr.practice
	}
	if rep.PracticeRuns > 0 {
		rep.Note = fmt.Sprintf("%d practice runs were left out: the scripted practice model answered them, so they are not findings.", rep.PracticeRuns)
	}

	// Benchmarks: clean condition only.
	ab := len(labels) == 2 && !score
	var verdicts []verdictab.BenchmarkVerdict
	var verdictRow []int        // rep.Benchmarks index of each verdict
	byCap := map[string][]int{} // capability -> indexes into rep.Benchmarks
	for _, cr := range children {
		b := Benchmark{Benchmark: cr.child.Benchmark, Capability: capOf(cr.child.Benchmark), ExperimentID: cr.child.ExperimentID, Arms: map[string]Metrics{}, ReliabilityCard: map[string]*ReliabilityCard{}}
		for _, l := range labels {
			clean := filter(cr.arms[l], schemas.ConditionClean)
			m := metricsOf(clean)
			m.Excluded = cr.excluded[l]
			b.Arms[l] = m
			if card := reliabilityCard(clean); card != nil {
				b.ReliabilityCard[l] = card
			}
		}
		if ab {
			v := compare(filter(cr.arms[labels[0]], schemas.ConditionClean), filter(cr.arms[labels[1]], schemas.ConditionClean))
			verdicts = append(verdicts, v)
			verdictRow = append(verdictRow, len(rep.Benchmarks))
		}
		byCap[b.Capability] = append(byCap[b.Capability], len(rep.Benchmarks))
		rep.Benchmarks = append(rep.Benchmarks, b)
	}
	if ab {
		// Many benchmarks are many tests: per-benchmark significance is
		// Holm-corrected across the evaluation (Overall applies the same).
		// Summaries name the agents, not the arm letters.
		na, nb := ArmNames(rep.Arms[0], rep.Arms[1])
		for k, v := range verdictab.Holm(verdicts) {
			holm := strings.HasSuffix(v.Summary, verdictab.HolmNote)
			v.Summary = verdictab.BenchmarkSummary(v, na, nb)
			if holm {
				v.Summary += verdictab.HolmNote
			}
			v = relabelVerdict(v, labels[0], labels[1])
			rep.Benchmarks[verdictRow[k]].Verdict = &v
		}
		o := verdictab.Overall(verdicts)
		o.Summary = verdictab.OverallSummary(o, na, nb)
		o = relabelOverall(o, labels[0], labels[1])
		rep.Overall = &o
	}

	// Capabilities: runs-weighted rollup of the benchmark rows.
	caps := make([]string, 0, len(byCap))
	for c := range byCap {
		caps = append(caps, c)
	}
	slices.Sort(caps)
	for _, c := range caps {
		row := Capability{Capability: c, Benchmarks: []string{}, Arms: map[string]Metrics{}}
		for _, i := range byCap[c] {
			row.Benchmarks = append(row.Benchmarks, rep.Benchmarks[i].Benchmark)
		}
		for _, l := range labels {
			parts := make([]Metrics, 0, len(byCap[c]))
			var cs []cluster
			for _, i := range byCap[c] {
				parts = append(parts, rep.Benchmarks[i].Arms[l])
				cs = append(cs, clustersOf(filter(children[i].arms[l], schemas.ConditionClean), rep.Benchmarks[i].Benchmark+"\x00")...)
			}
			row.Arms[l] = rollup(parts, cs)
		}
		rep.Capabilities = append(rep.Capabilities, row)
	}

	rep.Reliability = reliability(children, labels, faultOf)
	rep.Resilience = resilience(children, labels, faultOf)
	rep.Safety = safety(children, labels, faultOf)
	rep.Totals = totals(children, labels, faultOf)
	rep.Alignment = alignment(children, labels)
	rep.Learning = learningRows(children, labels)
	rep.Persistence = persistence(children, labels)
	rep.Voice = voiceRows(children, labels)
	return rep, nil
}

// readChild reads one child's graded runs by arm label. A run counts as the
// leaderboard counts it: the slot's first sealed run whose outcome is not
// aborted_infra, with an outcome that scores; compatibility runs never count.
func readChild(ctx context.Context, src leaderboard.Source, arms []schemas.EvaluationArm, c schemas.EvaluationChild) (childRuns, error) {
	cr := childRuns{child: c, arms: map[string][]graded{}, excluded: map[string]map[string]int{}, replays: map[string][]graded{}}
	if c.ExperimentID == "" {
		return cr, nil
	}
	m, ok := src.Manifest(c.ExperimentID)
	if !ok || m == nil {
		return cr, nil
	}
	if d, ok := src.LatestDecision(c.ExperimentID); ok && d == schemas.DecisionInvalid {
		return cr, nil
	}
	armLabel := mapArms(arms, m)
	practice, err := practiceArms(ctx, src, m, c.ExperimentID)
	if err != nil {
		return cr, err
	}
	cr.stream = m.Experiment.Stream
	freshID := ""
	if cr.stream != nil {
		freshID = cr.stream.FreshArm
		cr.freshArm = armLabel[freshID]
		if a, ok := m.Arms[freshID]; ok {
			cr.freshName = cmp.Or(a.Label, m.Experiment.Arms[freshID].Label)
		}
	}
	bySlot := map[string][]leaderboard.Run{}
	for _, r := range src.Runs(c.ExperimentID) {
		bySlot[r.Slot.SlotID] = append(bySlot[r.Slot.SlotID], r)
	}
	slots := make([]string, 0, len(bySlot))
	for s := range bySlot {
		slots = append(slots, s)
	}
	slices.Sort(slots)
	for _, s := range slots {
		if err := ctx.Err(); err != nil {
			return cr, fmt.Errorf("reportcard: %w", err)
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
		label, ok := armLabel[r.Slot.Arm]
		isFresh := freshID != "" && r.Slot.Arm == freshID
		if (!ok && !isFresh) || r.Slot.Arm == "compat" {
			continue
		}
		if _, scores := r.Run.Outcome.Score(); !scores {
			if why := schemas.ExcludedReason(&r.Run); why != "" && cmp.Or(r.Slot.Condition, schemas.ConditionClean) == schemas.ConditionClean {
				if cr.excluded[label] == nil {
					cr.excluded[label] = map[string]int{}
				}
				cr.excluded[label][why]++
			}
			continue
		}
		if practice[r.Slot.Arm] { // X01: the scripted practice model's numbers are not findings
			cr.practice++
			continue
		}
		metrics, err := readMetrics(src, r.Run.RunID)
		if err != nil {
			return cr, err
		}
		gates, err := readGates(src, r.Run.RunID)
		if err != nil {
			return cr, err
		}
		y, _ := leaderboard.VerifiedSuccess(r.Run.Outcome, metrics)
		cond := cmp.Or(r.Slot.Condition, schemas.ConditionClean)
		g := graded{task: r.Slot.Task, condition: cond, success: int(y), metrics: metrics, gates: gates, trial: r.Slot.Trial, streamIndex: r.Slot.StreamIndex, replay: r.Slot.StreamReplay}
		if isFresh {
			cr.fresh = append(cr.fresh, g)
		}
		switch {
		case !ok:
		case g.replay:
			cr.replays[label] = append(cr.replays[label], g)
		default:
			cr.arms[label] = append(cr.arms[label], g)
		}
	}
	return cr, nil
}

// practiceArms marks the experiment's arms whose runs are practice data, by
// the leaderboard's rule: a smoke or scripted_upstream experiment, a scripted
// control agent, or any sealed run of the arm labelled practice or whose
// proxy ledger shows the scripted model. One such run makes the whole arm
// practice, since an arm's runs share the proxy's upstream.
func practiceArms(ctx context.Context, src leaderboard.Source, m *schemas.Manifest, expID string) (map[string]bool, error) {
	out := map[string]bool{}
	for id, a := range m.Arms {
		out[id] = m.Experiment.NoPaidTraffic() || a.Config.Adapter == "scripted" || a.Config.Scripted != nil
	}
	for _, r := range src.Runs(expID) {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("reportcard: %w", err)
		}
		if !r.Sealed || out[r.Slot.Arm] {
			continue
		}
		if r.Run.Practice {
			out[r.Slot.Arm] = true
			continue
		}
		data, err := src.RunFile(r.Run.RunID, fileLedger)
		if err != nil {
			continue // no ledger: nothing shows the scripted model
		}
		if p, _ := leaderboard.LedgerProvenance(data); p == leaderboard.ProvenanceScripted || p == leaderboard.ProvenanceMixed {
			out[r.Slot.Arm] = true
		}
	}
	return out, nil
}

// mapArms maps the experiment's arm ids to evaluation arm labels: an arm id
// or manifest label equal to an evaluation label (any case), else the arms
// in sorted id order matched to the evaluation arms in order.
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
			if used[a.Label] {
				continue
			}
			if strings.EqualFold(id, a.Label) || strings.EqualFold(m.Arms[id].Label, a.Label) {
				out[id], used[a.Label] = a.Label, true
				break
			}
		}
	}
	if len(out) == len(ids) {
		return out
	}
	free := slices.DeleteFunc(slices.Clone(arms), func(a schemas.EvaluationArm) bool { return used[a.Label] })
	for _, id := range ids {
		if _, done := out[id]; done || len(free) == 0 {
			continue
		}
		out[id], free = free[0].Label, free[1:]
	}
	return out
}

// readMetrics reads a run's metrics.json; a missing file is no metrics.
func readMetrics(src leaderboard.Source, runID string) (map[string]schemas.Value, error) {
	data, err := src.RunFile(runID, fileMetrics)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reportcard: read %s of run %s: %w", fileMetrics, runID, err)
	}
	var out map[string]schemas.Value
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, nil // unreadable metrics leave the columns out, never fail the card
	}
	return out, nil
}

// readGates reads a run's gates.json as gate name -> fired; a missing or
// unreadable file is no gates (the safety section then reads "not tested").
func readGates(src leaderboard.Source, runID string) (map[string]bool, error) {
	data, err := src.RunFile(runID, fileGates)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reportcard: read %s of run %s: %w", fileGates, runID, err)
	}
	var results []schemas.GateResult
	if json.Unmarshal(data, &results) != nil {
		return nil, nil
	}
	out := make(map[string]bool, len(results))
	for _, g := range results {
		out[g.Gate] = out[g.Gate] || g.Fired
	}
	return out, nil
}

func filter(runs []graded, condition string) []graded {
	var out []graded
	for _, r := range runs {
		if r.condition == condition {
			out = append(out, r)
		}
	}
	return out
}

// metricsOf aggregates runs: success rate with its interval, sums of cost
// and tokens (nil unless every run knows them), medians of steps, tool
// calls and wall time over the runs that observed them.
func metricsOf(runs []graded) Metrics {
	m := Metrics{Runs: len(runs)}
	if len(runs) == 0 {
		return m
	}
	succ := 0
	for _, r := range runs {
		succ += r.success
	}
	m.Score = round(float64(succ) / float64(len(runs)))
	cs := clustersOf(runs, "")
	m.Tasks = len(cs)
	lo, hi := taskCI(cs)
	m.CILow, m.CIHigh = round(lo), round(hi)
	var cost, tokens float64
	cost, m.CostRuns, m.CostNote = sumKnown(runs, metricCostUSD)
	if m.CostRuns == len(runs) {
		cost = round(cost)
		m.CostUSD, m.CostNote = &cost, ""
	}
	tokens, m.TokenRuns, _ = sumKnown(runs, metricTokens)
	if m.TokenRuns == len(runs) {
		m.Tokens = &tokens
	}
	m.Steps = median(runs, metricSteps)
	m.ToolCalls = median(runs, metricToolCalls)
	m.WallS = round(median(runs, metricWallTime))
	m.Process = processOf(runs)
	return m
}

// processOf aggregates the process metrics of runs; nil when no run has a
// tool path score.
func processOf(runs []graded) *Process {
	matches := observed(runs, metricProcessMatch)
	if len(matches) == 0 {
		return nil
	}
	p := &Process{Runs: len(matches)}
	t := 0.0
	for _, x := range matches {
		t += x
	}
	v := round(t / float64(len(matches)))
	p.ToolMatch = &v
	if len(observed(runs, metricProcessRatio)) > 0 {
		r := round(median(runs, metricProcessRatio))
		p.StepRatio = &r
	}
	if len(observed(runs, metricProcessRedundant)) > 0 {
		r := round(median(runs, metricProcessRedundant))
		p.RedundantCalls = &r
	}
	return p
}

// rollupProcess weights each part's process scores by its scored runs.
func rollupProcess(parts []Metrics) *Process {
	var out *Process
	type acc struct{ sum, w float64 }
	var match, ratio, red acc
	add := func(a *acc, v *float64, w float64) {
		if v != nil {
			a.sum += *v * w
			a.w += w
		}
	}
	for _, p := range parts {
		if p.Process == nil || p.Process.Runs == 0 {
			continue
		}
		if out == nil {
			out = &Process{}
		}
		w := float64(p.Process.Runs)
		out.Runs += p.Process.Runs
		add(&match, p.Process.ToolMatch, w)
		add(&ratio, p.Process.StepRatio, w)
		add(&red, p.Process.RedundantCalls, w)
	}
	if out == nil {
		return nil
	}
	get := func(a acc) *float64 {
		if a.w == 0 {
			return nil
		}
		v := round(a.sum / a.w)
		return &v
	}
	out.ToolMatch, out.StepRatio, out.RedundantCalls = get(match), get(ratio), get(red)
	return out
}

func observed(runs []graded, id string) []float64 {
	var xs []float64
	for _, r := range runs {
		if v, ok := r.metrics[id].Float(); ok {
			xs = append(xs, v)
		}
	}
	return xs
}

// sumKnown sums metric id over the runs that observed it and counts them.
// note is the most common reason among the runs that did not (ties: the
// first in sort order), "not recorded" for a run with no value at all.
func sumKnown(runs []graded, id string) (total float64, known int, note string) {
	reasons := map[string]int{}
	for _, r := range runs {
		v, found := r.metrics[id]
		if x, ok := v.Float(); ok {
			total += x
			known++
			continue
		}
		reason := strings.TrimSpace(v.Reason)
		if !found || reason == "" {
			reason = "not recorded"
		}
		reasons[reason]++
	}
	best := 0
	for reason, n := range reasons {
		if n > best || (n == best && reason < note) {
			note, best = reason, n
		}
	}
	return total, known, note
}

func median(runs []graded, id string) float64 {
	xs := observed(runs, id)
	if len(xs) == 0 {
		return 0
	}
	return stats.Median(xs)
}

// rollup pools benchmark rows: successes and runs add up (so the score is
// weighted by runs), cost and tokens add up (nil when any row's is not
// known), medians are averaged weighted by runs. The interval is the
// task-clustered bootstrap over cs, every task of every benchmark in the
// capability.
func rollup(parts []Metrics, cs []cluster) Metrics {
	var out Metrics
	succ := 0.0
	cost, tokens := 0.0, 0.0
	costKnown, tokensKnown := true, true
	for _, p := range parts {
		if p.Runs == 0 {
			continue
		}
		w := float64(p.Runs)
		out.Runs += p.Runs
		out.Tasks += p.Tasks
		succ += p.Score * w
		if p.CostUSD != nil {
			cost += *p.CostUSD
		} else {
			costKnown = false
		}
		if p.Tokens != nil {
			tokens += *p.Tokens
		} else {
			tokensKnown = false
		}
		out.CostRuns += p.CostRuns
		out.TokenRuns += p.TokenRuns
		if out.CostNote == "" {
			out.CostNote = p.CostNote
		}
		out.Steps += p.Steps * w
		out.ToolCalls += p.ToolCalls * w
		out.WallS += p.WallS * w
	}
	for _, p := range parts {
		for k, n := range p.Excluded {
			if out.Excluded == nil {
				out.Excluded = map[string]int{}
			}
			out.Excluded[k] += n
		}
	}
	if out.Runs == 0 {
		return out
	}
	n := float64(out.Runs)
	out.Score = round(succ / n)
	lo, hi := taskCI(cs)
	out.CILow, out.CIHigh = round(lo), round(hi)
	if costKnown {
		cost = round(cost)
		out.CostUSD = &cost
	}
	if tokensKnown {
		out.Tokens = &tokens
	}
	out.Steps, out.ToolCalls, out.WallS = round(out.Steps/n), round(out.ToolCalls/n), round(out.WallS/n)
	out.Process = rollupProcess(parts)
	return out
}

// compare builds the verdictab input from two arms' clean runs. Each
// task's runs are numbered as trials in run order.
func compare(a, b []graded) verdictab.BenchmarkVerdict {
	return verdictab.Compare(outcomes(a), outcomes(b))
}

func outcomes(runs []graded) []verdictab.TaskOutcome {
	out := make([]verdictab.TaskOutcome, 0, len(runs))
	trial := map[string]int{}
	for _, r := range runs {
		trial[r.task]++
		out = append(out, verdictab.TaskOutcome{TaskID: r.task, Trial: trial[r.task], Success: r.success > 0, Score: float64(r.success)})
	}
	return out
}

// armLabel maps verdictab's fixed "A"/"B" to the evaluation's arm labels.
func armLabel(w, la, lb string) string {
	switch w {
	case verdictab.WinnerA:
		return la
	case verdictab.WinnerB:
		return lb
	}
	return w
}

// ArmNames are the names verdict summaries use for two arms: the label when
// it is more than a plain arm letter, else the agent with its version, and
// the model too when that is all that tells the two apart.
func ArmNames(a, b Arm) (string, string) {
	name := func(x Arm, withModel bool) string {
		if len(x.Label) > 1 {
			return x.Label
		}
		n := x.Agent
		if n == "" {
			n = x.Label
		}
		if x.Version > 0 {
			n += fmt.Sprintf(" v%d", x.Version)
		}
		if withModel && x.Model != "" {
			n += " on " + x.Model
		}
		return n
	}
	na, nb := name(a, false), name(b, false)
	if na == nb {
		na, nb = name(a, true), name(b, true)
	}
	if na == nb { // same agent, version and model: keep the letters apart
		na, nb = na+" ("+a.Label+")", nb+" ("+b.Label+")"
	}
	return na, nb
}

// relabelVerdict names the winner by the evaluation's arm labels when they
// are not plain A and B. The summary is written by the caller.
func relabelVerdict(v verdictab.BenchmarkVerdict, la, lb string) verdictab.BenchmarkVerdict {
	v.Winner = armLabel(v.Winner, la, lb)
	v.TaskDiffs = nil
	return v
}

func relabelOverall(o verdictab.OverallVerdict, la, lb string) verdictab.OverallVerdict {
	o.Winner = armLabel(o.Winner, la, lb)
	wins := make(map[string]int, len(o.Wins))
	for k, n := range o.Wins {
		wins[armLabel(k, la, lb)] += n
	}
	o.Wins = wins
	return o
}

// reliability scores each fault per arm, pooled over the benchmarks that ran
// it, with the drop from the same arm's clean score on those benchmarks.
func reliability(children []childRuns, labels []string, faultOf func(childRuns, string) string) []Reliability {
	type acc struct{ fs, fn, cs, cn int }
	byFault := map[string]map[string]*acc{}
	benches := map[string][]string{}
	for _, cr := range children {
		seen := map[string]bool{}
		for _, l := range labels {
			clean := filter(cr.arms[l], schemas.ConditionClean)
			cs := successes(clean)
			for _, r := range cr.arms[l] {
				f := faultOf(cr, r.condition)
				if f == "" || f == Attack || r.condition == schemas.ConditionClean {
					continue
				}
				if byFault[f] == nil {
					byFault[f] = map[string]*acc{}
				}
				a := byFault[f][l]
				if a == nil {
					a = &acc{}
					byFault[f][l] = a
				}
				a.fs += r.success
				a.fn++
				if !seen[f+"\x00"+l] {
					seen[f+"\x00"+l] = true
					a.cs += cs
					a.cn += len(clean)
				}
				if !slices.Contains(benches[f], cr.child.Benchmark) {
					benches[f] = append(benches[f], cr.child.Benchmark)
				}
			}
		}
	}
	faults := make([]string, 0, len(byFault))
	for f := range byFault {
		faults = append(faults, f)
	}
	slices.Sort(faults)
	out := make([]Reliability, 0, len(faults))
	for _, f := range faults {
		row := Reliability{Fault: f, Benchmarks: benches[f], Arms: map[string]FaultScore{}}
		slices.Sort(row.Benchmarks)
		for _, l := range labels {
			a := byFault[f][l]
			if a == nil || a.fn == 0 {
				continue
			}
			s := float64(a.fs) / float64(a.fn)
			fs := FaultScore{Score: round(s), Runs: a.fn}
			if a.cn > 0 {
				fs.Drop = round(s - float64(a.cs)/float64(a.cn))
			}
			row.Arms[l] = fs
		}
		out = append(out, row)
	}
	return out
}

// learningRows builds the learning summary of every stream benchmark per
// shown arm from its clean runs. The fresh-memory arm gets no row of its
// own: it is the forward transfer control of the others.
func learningRows(children []childRuns, labels []string) []LearningRow {
	var out []LearningRow
	for _, cr := range children {
		if cr.stream == nil {
			continue
		}
		fresh := points("fresh", filter(cr.fresh, schemas.ConditionClean))
		for _, l := range labels {
			if cr.freshArm != "" && l == cr.freshArm {
				continue
			}
			runs := append(filter(cr.arms[l], schemas.ConditionClean), filter(cr.replays[l], schemas.ConditionClean)...)
			carry := points(l, runs)
			if len(carry) == 0 {
				continue
			}
			var f []learning.Point
			if cr.freshArm != l {
				f = fresh
			}
			row := LearningRow{Benchmark: cr.child.Benchmark, Arm: l, Summary: learning.Summarize(carry, f, cr.stream.ThresholdOrDefault())}
			if len(f) > 0 {
				row.Control = cr.freshName
			}
			out = append(out, row)
		}
	}
	return out
}

// points turns stream runs into learning points; a run outside a stream is
// left out. The chain is the arm, condition and trial.
func points(arm string, runs []graded) []learning.Point {
	var out []learning.Point
	for _, r := range runs {
		if r.streamIndex < 1 {
			continue
		}
		out = append(out, learning.Point{
			Chain: fmt.Sprintf("%s\x00%s\x00%d", arm, r.condition, r.trial), Index: r.streamIndex,
			Task: r.task, Success: float64(r.success), Replay: r.replay,
		})
	}
	return out
}

// persistence averages the persistence metrics per arm over every counted
// run of every condition (the kill and squeeze faults are conditions).
func persistence(children []childRuns, labels []string) []PersistenceRow {
	var out []PersistenceRow
	for _, l := range labels {
		var runs []graded
		for _, cr := range children {
			runs = append(runs, cr.arms[l]...)
		}
		row := PersistenceRow{Arm: l, ResumedWithoutRedo: meanRate(runs, metricResumedWithoutRedo),
			GracefulFinish: meanRate(runs, metricGracefulFinish), CostNormalizedSuccess: meanRate(runs, metricCostNormalized)}
		if row.ResumedWithoutRedo != nil || row.GracefulFinish != nil || row.CostNormalizedSuccess != nil {
			out = append(out, row)
		}
	}
	return out
}

func meanRate(runs []graded, id string) *Rate {
	xs := observed(runs, id)
	if len(xs) == 0 {
		return nil
	}
	return &Rate{Value: round(stats.Mean(xs)), Runs: len(xs)}
}

func successes(runs []graded) int {
	n := 0
	for _, r := range runs {
		n += r.success
	}
	return n
}

// safety computes attack resisted and cheat rate per arm. Attack resisted
// reads the attack dimension on attacked runs: only a condition the
// benchmark's probe metadata marks as an attack (prompt injection and the
// like, FaultOf gives Attack) counts, never a fault or an unknown condition;
// runs that lack the dimension fall back to safe_success. Cheat rate is the
// mean of the observed cheating_rate values on clean runs only (impossible
// tasks): a fault changes what the agent sees, so faulted runs never mix in.
func safety(children []childRuns, labels []string, faultOf func(childRuns, string) string) Safety {
	s := Safety{AttackResisted: map[string]float64{}, CheatRate: map[string]float64{}}
	for _, l := range labels {
		var resisted, cheat []float64
		for _, cr := range children {
			for _, r := range cr.arms[l] {
				if r.condition == schemas.ConditionClean {
					if v, ok := r.metrics[metricCheatingRate].Float(); ok {
						cheat = append(cheat, v)
					}
				}
				if r.condition == schemas.ConditionClean || faultOf(cr, r.condition) != Attack {
					continue
				}
				if v, ok := r.metrics[metricAttackSucceded].Float(); ok {
					resisted = append(resisted, 1-v)
				} else if v, ok := r.metrics[metricSafeSuccess].Float(); ok {
					resisted = append(resisted, v)
				}
			}
		}
		if len(resisted) > 0 {
			s.AttackResisted[l] = round(stats.Mean(resisted))
		}
		if len(cheat) > 0 {
			s.CheatRate[l] = round(stats.Mean(cheat))
		}
	}
	return s
}

// Attack is what FaultOf returns for an attack probe (faults.Attack): its
// runs count only in Safety.AttackResisted.
const Attack = "attack"

// FaultIDs are the evaluation fault ids the fault catalog ships besides
// clean (bench/faults.Catalog). A condition id equal to one of them, or to
// the bench's generic probe for it ("heimdall-" and the id), is that fault.
var FaultIDs = func() []string {
	out := make([]string, 0, len(faults.Catalog))
	for _, f := range faults.Catalog {
		out = append(out, f.ID)
	}
	return out
}()

// DefaultFault is the fault of a condition when no benchmark metadata is at
// hand: only the fault names themselves (FaultIDs, or a compound- id) and
// the generic probes every benchmark shares ("heimdall-" and the fault id,
// such as schemas.GenericProbeModelDown). It never guesses from a probe's
// name; anything else is not a fault.
func DefaultFault(condition string) string {
	switch condition {
	case FaultModelDown, schemas.GenericProbeModelDown:
		return FaultModelDown
	case FaultBrokenTool, schemas.GenericProbeBrokenTool:
		return FaultBrokenTool
	case FaultMisleading:
		return FaultMisleading
	}
	if bare := strings.TrimPrefix(condition, "heimdall-"); slices.Contains(FaultIDs, bare) || strings.HasPrefix(bare, "compound-") {
		return bare
	}
	return ""
}

// knownCapability is the capability of the built-in library benchmarks
// (ui/components/suites/suiteDisplay.ts categories and suite tracks).
var knownCapability = map[string]string{
	"hd-harbor-tb2":    "terminal",
	"hd-harbor-skills": "terminal",
	"hd-swe-verified":  "coding",
	"hd-swe-rebench":   "coding",
	"hd-swe-ib-lcb":    "safety",
	"hd-swe-ib-swe":    "safety",
	"coding-core":      "coding",
	"longmemeval":      "memory",
	"locomo":           "memory",
	"tau2":             "support",
	"mcpmark":          "support",
	"gaia":             "research",
	"voicebench":       "voice",
}

// trackCapability maps a suite track to a capability.
var trackCapability = map[string]string{
	"coding": "coding", "artifact": "coding", "ml_engineering": "coding",
	"terminal": "terminal", "computer_use": "terminal",
	"memory":   "memory",
	"tool_use": "support", "mcp": "support", "support": "support",
	"research": "research", "browser": "research", "qa": "research",
	"voice":  "voice",
	"safety": "safety",
	"team":   "team", "multi_agent": "team",
	"gpu": "gpu",
}

// DefaultCapability names a benchmark's capability from its id alone.
func DefaultCapability(benchmark string) string {
	return CapabilityFor(benchmark, "")
}

// CapabilityFor names a benchmark's capability from its id and, when the id
// is not a known one, its suite track (empty track means coding).
func CapabilityFor(benchmark, track string) string {
	id, _, _ := strings.Cut(benchmark, "@")
	if c, ok := knownCapability[id]; ok {
		return c
	}
	switch {
	case strings.HasPrefix(id, "agentdojo"):
		return capabilitySafety
	case strings.HasPrefix(id, "longmemeval"), strings.HasPrefix(id, "locomo"):
		return "memory"
	case strings.HasPrefix(id, "browsecomp"), strings.HasPrefix(id, "gaia"):
		return "research"
	case strings.HasPrefix(id, "marble"):
		return "team"
	case strings.HasPrefix(id, "kernelbench"):
		return "coding"
	case strings.HasPrefix(id, "voice"):
		return "voice"
	}
	if track == "" {
		return "coding"
	}
	if c, ok := trackCapability[track]; ok {
		return c
	}
	return capabilityOther
}

func round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return math.Round(x*1e4) / 1e4
}
