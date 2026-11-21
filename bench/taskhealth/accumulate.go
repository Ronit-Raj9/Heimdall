package taskhealth

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// compatArm is the arm id of the compatibility agent (engine.CompatArm).
const compatArm = "compat"

// maxEvidenceRuns caps the run ids one evidence line lists.
const maxEvidenceRuns = 4

// accumulator gathers the observations of every experiment of one suite.
type accumulator struct {
	suite   *schemas.Suite
	tasks   map[string]*taskAcc
	exps    int
	runs    int
	notes   []string
	skipped int // runs of another task version
	broken  int // runs whose seal did not verify
}

// taskAcc is one task's observations.
type taskAcc struct {
	task      *schemas.Task
	attempts  int
	aborted   int
	infraErr  int
	invalid   int
	timeouts  int
	configs   map[string]*configAcc // non-synthetic scored runs, by config hash
	checks    int
	evidence  []schemas.VerifierEvidence
	outputs   map[outputKey]*outputGroup
	outputKey []outputKey // insertion order, so evidence order is stable
}

// configAcc is one agent config's scored runs on one task.
type configAcc struct {
	id, hash       string
	trials, passes int
}

// outputKey identifies "the same output graded by the same verifier": the
// output hash the grade recorded, under one suite and grader version.
type outputKey struct {
	suiteVersion, graderVersion, output string
}

// outputGroup is every run that produced one output.
type outputGroup struct {
	pass, fail []string
	exps       map[string]bool
}

func newAccumulator(s *schemas.Suite) *accumulator {
	a := &accumulator{suite: s, tasks: map[string]*taskAcc{}}
	for _, t := range s.Tasks {
		a.tasks[t.ID] = &taskAcc{task: t, configs: map[string]*configAcc{}, outputs: map[outputKey]*outputGroup{}}
	}
	return a
}

func (a *accumulator) notef(format string, args ...any) {
	a.notes = append(a.notes, fmt.Sprintf(format, args...))
}

// readExperiment adds one experiment: its counted sealed runs (difficulty,
// variance, oracle results, output groups), every attempt of its slots
// (infra) and its grader regrades (regrade disagreement).
func (a *accumulator) readExperiment(ctx context.Context, src Source, m *schemas.Manifest) error {
	a.exps++
	runs, err := src.SealedRuns(ctx, m.ExpID, "")
	if err != nil {
		return fmt.Errorf("sealed runs: %w", err)
	}
	original := map[string]*schemas.SealedRun{}
	for i := range runs {
		sr := &runs[i]
		ta := a.taskOf(sr.Slot)
		if ta == nil {
			continue
		}
		if len(sr.Problems) > 0 {
			a.broken++
			continue
		}
		original[sr.Run.RunID] = sr
		cfg := armConfig(m, sr.Slot.Arm)
		ta.addRun(m, sr, cfg, synthetic(m, cfg))
	}
	a.readAttempts(ctx, src, m)
	return a.readRegrades(ctx, src, m.ExpID, original)
}

// taskOf returns the accumulator of a slot's task, or nil when the task is
// not in the suite or the run used another task version.
func (a *accumulator) taskOf(slot schemas.Slot) *taskAcc {
	ta := a.tasks[slot.Task]
	if ta == nil {
		return nil
	}
	if slot.TaskVersion != "" && slot.TaskVersion != ta.task.Version {
		a.skipped++
		return nil
	}
	return ta
}

// addRun records one counted sealed run.
func (ta *taskAcc) addRun(m *schemas.Manifest, sr *schemas.SealedRun, cfg *schemas.AgentConfig, syn bool) {
	g := sr.Grade
	if g != nil && g.DiffSHA256 != "" && !g.InvalidEvaluator {
		k := outputKey{g.SuiteVersion, g.GraderVersion, g.DiffSHA256}
		grp := ta.outputs[k]
		if grp == nil {
			grp = &outputGroup{exps: map[string]bool{}}
			ta.outputs[k] = grp
			ta.outputKey = append(ta.outputKey, k)
		}
		grp.exps[m.ExpID] = true
		if g.Passed {
			grp.pass = append(grp.pass, sr.Run.RunID)
		} else {
			grp.fail = append(grp.fail, sr.Run.RunID)
		}
	}
	if oracle(cfg) {
		ta.checks++
		if g != nil && !g.Passed && !g.InvalidEvaluator {
			ta.evidence = append(ta.evidence, schemas.VerifierEvidence{
				Kind:   schemas.EvidenceOracleFailed,
				Detail: fmt.Sprintf("the scripted oracle (reference solution) failed under condition %s", sr.Slot.Condition),
				ExpID:  m.ExpID, RunIDs: []string{sr.Run.RunID},
			})
		}
	}
	if syn {
		return
	}
	y, ok := sr.Run.Outcome.Score()
	if !ok {
		return
	}
	hash := sr.Run.Agent.ConfigHash
	if hash == "" {
		hash = sr.Run.Agent.ConfigID
	}
	c := ta.configs[hash]
	if c == nil {
		c = &configAcc{id: sr.Run.Agent.ConfigID, hash: hash}
		ta.configs[hash] = c
	}
	c.trials++
	if y == 1 {
		c.passes++
	}
}

// readAttempts counts every sealed attempt of the experiment's slots by
// outcome, aborted_infra retries included. A slot that cannot be read is a
// note, not a failure: infra counts are a lower bound then.
func (a *accumulator) readAttempts(ctx context.Context, src Source, m *schemas.Manifest) {
	unread := 0
	for _, slot := range m.Slots {
		ta := a.tasks[slot.Task]
		if ta == nil || (slot.TaskVersion != "" && slot.TaskVersion != ta.task.Version) {
			continue
		}
		recs, err := src.SlotAttempts(ctx, m.ExpID, slot.SlotID)
		if err != nil {
			unread++
			continue
		}
		for _, r := range recs {
			if !r.Sealed {
				continue
			}
			a.runs++
			ta.attempts++
			switch r.Run.Outcome {
			case schemas.OutcomeAbortedInfra:
				ta.aborted++
			case schemas.OutcomeInfraError:
				ta.infraErr++
			case schemas.OutcomeInvalidEvaluator:
				ta.invalid++
			case schemas.OutcomeTimeout:
				ta.timeouts++
			}
		}
	}
	if unread > 0 {
		a.notef("experiment %s: %d slots could not be read; infra counts are a lower bound", m.ExpID, unread)
	}
}

// readRegrades compares every grader regrade of the experiment with the
// original grades. Only a regrade at the same suite and grader version on the
// same output hash is a like-for-like re-grade; a flipped verdict there is
// verifier flakiness. A regrade under a new grader or suite version is a
// deliberate change and says nothing about flakiness.
func (a *accumulator) readRegrades(ctx context.Context, src Source, expID string, original map[string]*schemas.SealedRun) error {
	rgs, err := src.Regrades(ctx, expID)
	if err != nil {
		return fmt.Errorf("regrades: %w", err)
	}
	for _, rg := range rgs {
		if rg.GraderVersion == "" {
			continue
		}
		runs, err := src.SealedRuns(ctx, expID, rg.ID)
		if err != nil {
			a.notef("experiment %s: regrade %s skipped: %v", expID, rg.ID, err)
			continue
		}
		for i := range runs {
			r := &runs[i]
			o := original[r.Run.RunID]
			if o == nil || o.Grade == nil || r.Grade == nil || r.GradeSource != rg.ID || len(r.Problems) > 0 {
				continue
			}
			og, rgd := o.Grade, r.Grade
			if og.SuiteVersion != rgd.SuiteVersion || og.GraderVersion != rgd.GraderVersion || og.DiffSHA256 != rgd.DiffSHA256 {
				continue
			}
			ta := a.tasks[r.Slot.Task]
			ta.checks++
			if og.Passed != rgd.Passed || og.InvalidEvaluator != rgd.InvalidEvaluator {
				ta.evidence = append(ta.evidence, schemas.VerifierEvidence{
					Kind: schemas.EvidenceRegradeDisagrees,
					Detail: fmt.Sprintf("regrade %s graded the same output %s, the original %s (grader %s)",
						rg.ID, verdict(rgd), verdict(og), og.GraderVersion),
					ExpID: expID, RunIDs: []string{r.Run.RunID},
				})
			}
		}
	}
	return nil
}

// admission adds the reference repeats of each task's last admission: the
// reference is graded several times in a row, and any result other than pass
// is the verifier failing the known-good answer.
func (a *accumulator) admission() {
	if a.suite.Lock == nil {
		return
	}
	for _, t := range a.suite.Tasks {
		tl, ok := a.suite.Lock.Tasks[t.ID]
		if !ok || tl.Admission == nil || len(tl.Admission.ReferenceResults) == 0 || tl.Version != t.Version {
			continue
		}
		ta := a.tasks[t.ID]
		ta.checks++
		pass := 0
		for _, r := range tl.Admission.ReferenceResults {
			if r == "pass" {
				pass++
			}
		}
		if pass == len(tl.Admission.ReferenceResults) {
			continue
		}
		what := "inconsistent"
		if pass == 0 {
			what = "never passing"
		}
		ta.evidence = append(ta.evidence, schemas.VerifierEvidence{
			Kind: schemas.EvidenceAdmissionRef,
			Detail: fmt.Sprintf("admission graded the reference %s over %d repeats (%s): %s",
				strings.Join(tl.Admission.ReferenceResults, ", "), len(tl.Admission.ReferenceResults), tl.Admission.At.Format("2006-01-02"), what),
		})
	}
}

// outputEvidence turns each output group with both verdicts into evidence.
func (ta *taskAcc) outputEvidence() {
	for _, k := range ta.outputKey {
		g := ta.outputs[k]
		if len(g.pass)+len(g.fail) < 2 {
			continue
		}
		ta.checks++
		if len(g.pass) == 0 || len(g.fail) == 0 {
			continue
		}
		exps := make([]string, 0, len(g.exps))
		for e := range g.exps {
			exps = append(exps, e)
		}
		sort.Strings(exps)
		runs := append(capRuns(g.pass), capRuns(g.fail)...)
		ev := schemas.VerifierEvidence{
			Kind: schemas.EvidenceSameDiffDiffers,
			Detail: fmt.Sprintf("output %s graded pass %d times and fail %d times by grader %s",
				short(k.output), len(g.pass), len(g.fail), k.graderVersion),
			RunIDs: runs,
		}
		if len(exps) == 1 {
			ev.ExpID = exps[0]
		} else {
			ev.Detail += " across experiments " + strings.Join(exps, ", ")
		}
		ta.evidence = append(ta.evidence, ev)
	}
}

// armConfig returns the resolved agent config of an arm of the manifest.
func armConfig(m *schemas.Manifest, arm string) *schemas.AgentConfig {
	if arm == compatArm && m.Compatibility != nil {
		return &m.Compatibility.Config
	}
	if a, ok := m.Arms[arm]; ok {
		return &a.Config
	}
	return nil
}

// synthetic reports whether runs of cfg in m are synthetic: a declared smoke
// experiment, or the scripted adapter. Synthetic runs are left out of
// difficulty and variance but still count for infra and, when the agent is
// the scripted oracle, for verifier flakiness.
func synthetic(m *schemas.Manifest, cfg *schemas.AgentConfig) bool {
	return m.Experiment.NoPaidTraffic() || cfg == nil || cfg.Adapter == "scripted"
}

// oracle reports whether cfg is the scripted oracle, which applies the
// task's reference solution.
func oracle(cfg *schemas.AgentConfig) bool {
	return cfg != nil && cfg.Adapter == "scripted" && cfg.Scripted != nil && cfg.Scripted.Kind == "oracle"
}

func verdict(g *schemas.Grade) string {
	switch {
	case g.InvalidEvaluator:
		return "invalid_evaluator"
	case g.Passed:
		return "pass"
	default:
		return "fail"
	}
}

func capRuns(ids []string) []string {
	if len(ids) > maxEvidenceRuns/2 {
		ids = ids[:maxEvidenceRuns/2]
	}
	return append([]string(nil), ids...)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// result builds the report from the accumulated observations.
func (a *accumulator) result(o Options, quarantined map[string]schemas.TaskQuarantine) *schemas.SuiteHealth {
	h := &schemas.SuiteHealth{
		Suite: a.suite.ID, Version: a.suite.Version, ComputedAt: o.Now().UTC(),
		Experiments: a.exps, Runs: a.runs,
		Thresholds: schemas.HealthThresholds{MinRuns: o.MinRuns, MinConfigs: o.MinConfigs, EasyAbove: o.EasyAbove,
			HardBelow: o.HardBelow, InfraRate: o.InfraRate, TimeoutRate: o.TimeoutRate, IntervalLevel: intervalLevel},
		Tasks:  make([]schemas.TaskHealth, 0, len(a.suite.Tasks)),
		Counts: map[string]int{},
		Notes:  append([]string{}, a.notes...),
	}
	if a.skipped > 0 {
		h.Notes = append(h.Notes, fmt.Sprintf("%d runs of other task versions were left out", a.skipped))
	}
	if a.broken > 0 {
		h.Notes = append(h.Notes, fmt.Sprintf("%d runs whose seal did not verify were left out", a.broken))
	}
	for _, t := range a.suite.Tasks {
		ta := a.tasks[t.ID]
		ta.outputEvidence()
		th := ta.health(o)
		if q, ok := quarantined[t.ID]; ok {
			th.Quarantine = &q
		}
		for _, f := range th.Flags {
			h.Counts[f]++
		}
		h.Tasks = append(h.Tasks, th)
	}
	return h
}

// health is one task's report.
func (ta *taskAcc) health(o Options) schemas.TaskHealth {
	split := ta.task.Split
	if split == "" {
		split = schemas.SplitPublic
	}
	th := schemas.TaskHealth{
		Task: ta.task.ID, Split: split,
		Verifier: schemas.VerifierHealth{Flaky: len(ta.evidence) > 0, Checks: ta.checks,
			Evidence: append([]schemas.VerifierEvidence{}, ta.evidence...)},
		Variance: ta.variance(),
		Infra:    ta.infra(),
	}
	th.Difficulty = ta.difficulty(o)
	flags := map[string]bool{}
	if th.Verifier.Flaky {
		flags[schemas.HealthFlakyVerifier] = true
	}
	d := th.Difficulty
	enough := d.Runs >= o.MinRuns && d.Configs >= o.MinConfigs
	switch {
	case !enough:
		flags[schemas.HealthInsufficientData] = true
	case d.Discrimination == "always_pass":
		flags[schemas.HealthSaturated], flags[schemas.HealthNoDiscrimination] = true, true
	case d.Discrimination == "always_fail":
		flags[schemas.HealthDead], flags[schemas.HealthNoDiscrimination] = true, true
	}
	if d.Match == "mismatch" {
		flags[schemas.HealthDifficultyMismatch] = true
	}
	if th.Infra.Attempts > 0 && th.Infra.Rate > o.InfraRate {
		flags[schemas.HealthInfraTrouble] = true
	}
	if th.Infra.Attempts > 0 && th.Infra.TimeoutRate > o.TimeoutRate {
		flags[schemas.HealthTimeouts] = true
	}
	th.Flags = sortedFlags(flags)
	return th
}

// sortedConfigs returns the task's configs by config id, then hash.
func (ta *taskAcc) sortedConfigs() []*configAcc {
	out := make([]*configAcc, 0, len(ta.configs))
	for _, c := range ta.configs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].id != out[j].id {
			return out[i].id < out[j].id
		}
		return out[i].hash < out[j].hash
	})
	return out
}

// variance is agent outcome variance per config: configs with at least two
// scored trials, how many of them both passed and failed, and the mean
// Bernoulli variance p(1-p) across them.
func (ta *taskAcc) variance() schemas.OutcomeVariance {
	v := schemas.OutcomeVariance{PerConfig: []schemas.ConfigOutcome{}}
	sum := 0.0
	for _, c := range ta.sortedConfigs() {
		p := float64(c.passes) / float64(c.trials)
		v.PerConfig = append(v.PerConfig, schemas.ConfigOutcome{Config: c.id, ConfigHash: c.hash, Trials: c.trials, Passes: c.passes, PassRate: p})
		if c.trials < 2 {
			continue
		}
		v.Configs++
		sum += p * (1 - p)
		if c.passes > 0 && c.passes < c.trials {
			v.Unstable++
		}
	}
	if v.Configs > 0 {
		v.MeanVariance = sum / float64(v.Configs)
	}
	return v
}

// infra is the share of attempts that ended outside the agent's control.
func (ta *taskAcc) infra() schemas.InfraHealth {
	h := schemas.InfraHealth{Attempts: ta.attempts, AbortedInfra: ta.aborted, InfraError: ta.infraErr,
		InvalidEvaluator: ta.invalid, Timeouts: ta.timeouts}
	if ta.attempts > 0 {
		h.Rate = float64(ta.aborted+ta.infraErr+ta.invalid) / float64(ta.attempts)
		h.TimeoutRate = float64(ta.timeouts) / float64(ta.attempts)
	}
	return h
}

// difficulty pools every non-synthetic scored run of the task.
func (ta *taskAcc) difficulty(o Options) schemas.DifficultyHealth {
	d := schemas.DifficultyHealth{Declared: ta.task.Difficulty, Observed: "unknown", Discrimination: "unknown"}
	passes, minRate, maxRate := 0, 1.0, 0.0
	for _, c := range ta.configs {
		d.Runs += c.trials
		passes += c.passes
		r := float64(c.passes) / float64(c.trials)
		minRate, maxRate = min(minRate, r), max(maxRate, r)
	}
	d.Configs = len(ta.configs)
	if d.Runs > 0 {
		rate := float64(passes) / float64(d.Runs)
		d.PassRate = &rate
		if lo, hi, ok := Wilson(passes, d.Runs); ok {
			d.Interval = &[2]float64{lo, hi}
		}
		d.Observed = DifficultyLabel(rate, d.Runs, d.Configs, o)
		spread := maxRate - minRate
		d.Spread = &spread
	}
	switch {
	case d.Configs == 0:
	case d.Configs == 1:
		d.Discrimination = "single_config"
	case minRate == 1:
		d.Discrimination = "always_pass"
	case maxRate == 0:
		d.Discrimination = "always_fail"
	default:
		d.Discrimination = "discriminating"
	}
	switch {
	case d.Declared == "":
		d.Match = "undeclared"
	case d.Observed == "unknown":
		d.Match = "unknown"
	case d.Declared == d.Observed:
		d.Match = "match"
	default:
		d.Match = "mismatch"
	}
	return d
}
