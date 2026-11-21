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

	"github.com/maximhq/bifrost/bench/leaderboard"
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
)

// Conf is the level of the score interval.
const Conf = 0.95

// Faults an evaluation can ask for besides clean.
const (
	FaultModelDown   = "model-down"
	FaultBrokenTool  = "broken-tool"
	FaultMisleading  = "misleading-instruction"
	fileMetrics      = "metrics.json"
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
	Mode         string                    `json:"mode,omitempty"` // the evaluation's mode; "score" has one arm and no verdict
	Arms         []Arm                     `json:"arms"`
	Capabilities []Capability              `json:"capabilities"`
	Benchmarks   []Benchmark               `json:"benchmarks"`
	Reliability  []Reliability             `json:"reliability"`
	Safety       Safety                    `json:"safety"`
	Overall      *verdictab.OverallVerdict `json:"overall,omitempty"`
}

// Options tune Build. Zero values take the defaults.
type Options struct {
	// CapabilityOf names the capability of a benchmark (suite id). nil uses
	// DefaultCapability.
	CapabilityOf func(benchmark string) string
	// FaultOf maps an experiment condition id to an evaluation fault, "" for
	// a condition that is not a fault (an attack probe, say). nil uses
	// DefaultFault.
	FaultOf func(condition string) string
	// Arms keeps only these arm labels; empty keeps every arm.
	Arms []string
}

// graded is one counted, sealed, scored run.
type graded struct {
	task      string
	condition string
	success   int
	metrics   map[string]schemas.Value
}

// childRuns is one child experiment's graded runs by arm label.
type childRuns struct {
	child schemas.EvaluationChild
	arms  map[string][]graded
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
	faultOf := opt.FaultOf
	if faultOf == nil {
		faultOf = DefaultFault
	}
	rep := &Report{
		Evaluation: ev.ID, Status: ev.Status, Mode: ev.Spec.Mode, Arms: []Arm{}, Capabilities: []Capability{}, Benchmarks: []Benchmark{},
		Reliability: []Reliability{}, Safety: Safety{AttackResisted: map[string]float64{}, CheatRate: map[string]float64{}},
	}
	// Score mode runs a hidden repeat of the one arm (to satisfy the
	// planner's two-arm rule); the report shows only the arm asked for and
	// never an A/B verdict.
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
	}

	// Benchmarks: clean condition only.
	ab := len(labels) == 2 && !score
	var verdicts []verdictab.BenchmarkVerdict
	var verdictRow []int        // rep.Benchmarks index of each verdict
	byCap := map[string][]int{} // capability -> indexes into rep.Benchmarks
	for _, cr := range children {
		b := Benchmark{Benchmark: cr.child.Benchmark, Capability: capOf(cr.child.Benchmark), ExperimentID: cr.child.ExperimentID, Arms: map[string]Metrics{}}
		for _, l := range labels {
			b.Arms[l] = metricsOf(filter(cr.arms[l], schemas.ConditionClean))
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
		for k, v := range verdictab.Holm(verdicts) {
			v = relabelVerdict(v, labels[0], labels[1])
			rep.Benchmarks[verdictRow[k]].Verdict = &v
		}
		o := relabelOverall(verdictab.Overall(verdicts), labels[0], labels[1])
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
	rep.Safety = safety(children, labels, faultOf)
	return rep, nil
}

// readChild reads one child's graded runs by arm label. A run counts as the
// leaderboard counts it: the slot's first sealed run whose outcome is not
// aborted_infra, with an outcome that scores; compatibility runs never count.
func readChild(ctx context.Context, src leaderboard.Source, arms []schemas.EvaluationArm, c schemas.EvaluationChild) (childRuns, error) {
	cr := childRuns{child: c, arms: map[string][]graded{}}
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
		i := slices.IndexFunc(runs, func(r leaderboard.Run) bool { return r.Sealed && r.Run.Outcome != schemas.OutcomeAbortedInfra })
		if i < 0 {
			continue
		}
		r := runs[i]
		label, ok := armLabel[r.Slot.Arm]
		if !ok || r.Slot.Arm == "compat" {
			continue
		}
		if _, scores := r.Run.Outcome.Score(); !scores {
			continue
		}
		metrics, err := readMetrics(src, r.Run.RunID)
		if err != nil {
			return cr, err
		}
		y, _ := leaderboard.VerifiedSuccess(r.Run.Outcome, metrics)
		cond := cmp.Or(r.Slot.Condition, schemas.ConditionClean)
		cr.arms[label] = append(cr.arms[label], graded{task: r.Slot.Task, condition: cond, success: int(y), metrics: metrics})
	}
	return cr, nil
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
	return m
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

// relabelVerdict names the winner by the evaluation's arm labels when they
// are not plain A and B. The summary keeps verdictab's wording.
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
func reliability(children []childRuns, labels []string, faultOf func(string) string) []Reliability {
	type acc struct{ fs, fn, cs, cn int }
	byFault := map[string]map[string]*acc{}
	benches := map[string][]string{}
	for _, cr := range children {
		seen := map[string]bool{}
		for _, l := range labels {
			clean := filter(cr.arms[l], schemas.ConditionClean)
			cs := successes(clean)
			for _, r := range cr.arms[l] {
				f := faultOf(r.condition)
				if f == "" || r.condition == schemas.ConditionClean {
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

func successes(runs []graded) int {
	n := 0
	for _, r := range runs {
		n += r.success
	}
	return n
}

// safety computes attack resisted and cheat rate per arm. Attack resisted
// reads the AgentDojo attack dimension on attacked runs (a non-clean
// condition that is not an evaluation fault); a benchmark whose runs lack
// the dimension falls back to safe_success on those runs. Cheat rate is the
// mean of the observed cheating_rate values (impossible tasks only).
func safety(children []childRuns, labels []string, faultOf func(string) string) Safety {
	s := Safety{AttackResisted: map[string]float64{}, CheatRate: map[string]float64{}}
	for _, l := range labels {
		var resisted, cheat []float64
		for _, cr := range children {
			adj := isAgentDojo(cr.child.Benchmark)
			for _, r := range cr.arms[l] {
				if v, ok := r.metrics[metricCheatingRate].Float(); ok {
					cheat = append(cheat, v)
				}
				if !adj || r.condition == schemas.ConditionClean || faultOf(r.condition) != "" {
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

func isAgentDojo(benchmark string) bool { return strings.HasPrefix(benchmark, "agentdojo") }

// DefaultFault maps a condition id to an evaluation fault by name: the
// fault's own id, or a condition id naming an outage, a tool fault or a
// misleading or stale instruction. Anything else is not a fault.
func DefaultFault(condition string) string {
	c := strings.ToLower(condition)
	switch {
	case c == "" || c == schemas.ConditionClean:
		return ""
	case c == FaultModelDown || strings.Contains(c, "model-down") || strings.Contains(c, "model_down") || strings.Contains(c, "outage"):
		return FaultModelDown
	case c == FaultBrokenTool || strings.Contains(c, "broken-tool") || strings.Contains(c, "broken_tool") || strings.Contains(c, "tool-error") || strings.Contains(c, "tool_error") || strings.Contains(c, "toolfault"):
		return FaultBrokenTool
	case c == FaultMisleading || strings.Contains(c, "mislead") || strings.Contains(c, "stale"):
		return FaultMisleading
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
