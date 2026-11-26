package improve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/maximhq/bifrost/bench/stats"
)

func init() {
	RunExtend = func(r *Runner, ctx context.Context, id string, extraTrials int, newTasks bool, extraUSD float64) (*Record, error) {
		return r.extend(ctx, id, extraTrials, newTasks, extraUSD)
	}
}

// Files the end of the loop keeps beside the record: the counted runs of the
// final experiment and of the second look, which the claim and the second
// look read again.
const (
	FinalRunsFile     = "final_runs.json"
	ExtensionRunsFile = "extension_runs.json"
)

// maxNewTasks caps the benchmark tasks a second look adds (A2.11).
const maxNewTasks = 200

// finish runs the end of the loop: the null improver, the final interleaved
// experiment on the held-out tasks, the retries control, then the claim.
// Nothing runs when no round kept a version (A2.4) or the run is a loop of a
// study. Each part is saved, so a resumed run skips what is done.
func (r *Runner) finish(ctx context.Context, rec *Record) error {
	if rec.Controls == nil {
		rec.Controls = &Controls{}
	}
	c := rec.Controls
	if committed(rec) && !rec.InStudy {
		if wantControl(&rec.Spec, ControlNullImprover) && c.Null == nil {
			nc, err := r.nullImprover(ctx, rec)
			if err != nil {
				return err
			}
			c.Null = nc
			if err := r.saveLive(rec); err != nil {
				return err
			}
		}
		if rec.Final == nil {
			if err := r.final(ctx, rec); err != nil {
				return err
			}
			if err := r.saveLive(rec); err != nil {
				return err
			}
		}
		if wantControl(&rec.Spec, ControlRetries) && c.Retries == nil {
			runs, err := r.loadRuns(rec.ID, FinalRunsFile)
			if err != nil {
				return err
			}
			rc, err := r.retries(ctx, rec, runs)
			if err != nil {
				return err
			}
			c.Retries = rc
			if err := r.saveLive(rec); err != nil {
				return err
			}
		}
	}
	v, err := r.verdict(rec)
	if err != nil {
		return err
	}
	rec.Verdict = v
	return nil
}

func committed(rec *Record) bool {
	return slices.ContainsFunc(rec.Rounds, func(rd *Round) bool { return rd.Decision == DecisionCommitted })
}

// final is the one experiment the claim reads (A2.4): A = version 0, B = the
// final version, C = the null improver's final version when it ran and
// changed something, all on the held-out tasks in one experiment.
func (r *Runner) final(ctx context.Context, rec *Record) error {
	arms := []Version{rec.Base, rec.Incumbent}
	nc := rec.Controls.Null
	if nc != nil && nc.Final.BundleHash != rec.Base.BundleHash {
		arms = append(arms, nc.Final)
	}
	if err := r.measure(rec, nil, PhaseFinal, PhaseFinal, arms, rec.Splits.Sealed, rec.Spec.Sealed.Trials, mainLevel(&rec.Spec)); err != nil {
		return err
	}
	if err := r.hardStop(rec, "the final check", scoreCost(rec, len(arms), len(rec.Splits.Sealed), rec.Spec.Sealed.Trials)); err != nil {
		return err
	}
	res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Phase: PhaseFinal,
		Arms: arms, Tasks: rec.Splits.Sealed, Trials: rec.Spec.Sealed.Trials})
	if err != nil {
		return fmt.Errorf("final check on held-out tasks: %w", err)
	}
	if err := r.saveRuns(rec.ID, FinalRunsFile, res.Runs); err != nil {
		return err
	}
	rec.Controls.CostUSD += res.CostUSD
	fc := &FinalClaim{ExpID: res.ExpID, Trials: rec.Spec.Sealed.Trials, Arms: []string{}}
	for _, a := range arms {
		fc.Arms = append(fc.Arms, a.Ref)
	}
	rec.Final = fc
	if nc != nil && len(arms) == 3 {
		b, _ := TaskMeans(res.Runs, 1, rec.Splits.Sealed)
		cm, _ := TaskMeans(res.Runs, 2, rec.Splits.Sealed)
		f := TaskDiffs(cm, b, rec.Splits.Sealed)
		nc.ExpID, nc.Score, nc.Diff = res.ExpID, score(res.Runs, 2), mean(f)
		if lo, hi, ok := stats.PairedT(f, 0.80); ok {
			nc.CI80 = [2]float64{lo, hi}
		}
	}
	return nil
}

// verdict runs the claim rule on the final experiment and adds the loop's
// counts and the control lines. Wording, when set, is called by the claim
// rule and reads the controls from the record.
func (r *Runner) verdict(rec *Record) (*Verdict, error) {
	runs := []RunScore{}
	if rec.Final != nil {
		var err error
		if runs, err = r.loadRuns(rec.ID, FinalRunsFile); err != nil {
			return nil, err
		}
	}
	in := r.claimInput(rec, runs, rec.Splits.Sealed)
	in.Trials = rec.Spec.Sealed.Trials
	v := r.claimRule().Decide(in)
	if rec.Final != nil {
		fillFinal(rec.Final, v)
	}
	r.decorate(rec, v)
	return v, nil
}

// claimInput builds the claim's input from counted runs on tasks.
func (r *Runner) claimInput(rec *Record, runs []RunScore, tasks []string) ClaimInput {
	ts := slices.Clone(tasks)
	slices.Sort(ts)
	a, _ := TaskMeans(runs, 0, ts)
	b, _ := TaskMeans(runs, 1, ts)
	in := ClaimInput{Spec: &rec.Spec, Rec: rec, A: a, B: b, Tasks: ts, Committed: committed(rec), Version: rec.Incumbent.Number}
	in.Missing = len(ts) - len(TaskDiffs(a, b, ts))
	for _, rd := range rec.Rounds {
		if len(rd.Approvals) > 0 && !in.Approved {
			in.Approved, in.ApprovedRound = true, rd.N
		}
	}
	return in
}

func fillFinal(fc *FinalClaim, v *Verdict) {
	fc.N, fc.NNonzero, fc.Gain, fc.PUpper, fc.PLower = v.N, v.NNonzero, v.Gain, v.PUpper, v.PLower
	fc.MDE, fc.Level, fc.CI80, fc.CI95 = v.MDE, v.Level, v.CI80, v.CI95
}

// decorate adds the loop's counts, spend, control lines and notes to a
// verdict, and a plain headline when no wording is installed.
func (r *Runner) decorate(rec *Record, v *Verdict) {
	if v.Notes == nil {
		v.Notes = []string{}
	}
	for _, rd := range rec.Rounds {
		switch rd.Decision {
		case DecisionCommitted:
			v.Commits++
		case DecisionRejected:
			v.Rejects++
		}
		if len(rd.Tripwires) > 0 {
			v.Tripwires++
		}
		v.Overrides += len(rd.Approvals)
		if rd.Audit != nil && rd.Audit.Greedy && rd.Decision != DecisionCommitted {
			v.GreedyExtra++
		}
	}
	v.TotalUSD = totalUSD(rec)
	if rec.Baseline != nil {
		if dev := finalDev(rec); dev != nil {
			v.DevGain = dev.Mean - rec.Baseline.Dev.Mean
		}
	}
	v.GenGap = v.DevGain - v.SealedGain
	if v.TotalUSD > 0 {
		g := v.SealedGain / v.TotalUSD
		v.GainPerUSD = &g
	}
	v.Teacher = rec.Spec.Improver.Model
	if c := rec.Controls; c != nil {
		if nc := c.Null; nc != nil && rec.Final != nil {
			if len(rec.Final.Arms) == 3 {
				v.Null = &ControlLine{Score: nc.Score.Mean, Diff: nc.Diff, CI80: nc.CI80}
			} else {
				v.Null = &ControlLine{Note: "Edits made without feedback changed nothing."}
			}
		}
		if rc := c.Retries; rc != nil {
			v.Retries = &ControlLine{Score: rc.Score, Diff: rc.Diff, CI80: rc.CI80}
		}
	}
	if why := earlyStop(rec); why != "" && len(rec.Rounds) < rec.Spec.Rounds {
		v.Notes = append(v.Notes, "the loop stopped early: "+why)
	}
	if v.GreedyExtra > 0 {
		v.Notes = append(v.Notes, fmt.Sprintf("keeping every version that scored higher would have kept %d more the gate test refused", v.GreedyExtra))
	}
	if v.Headline == "" {
		v.Headline = plainHeadline(v)
	}
}

// plainHeadline is the headline when no wording is installed.
func plainHeadline(v *Verdict) string {
	if v.State == VerdictNoChange {
		return "No new version was kept: none passed the gate. The agent stays at version 0."
	}
	return fmt.Sprintf("Finished (%s): %s points on %d held-out tasks.", v.State, formatPoints(v.Gain), v.N)
}

// finalDev is the incumbent's last practice score from a screen.
func finalDev(rec *Record) *ArmScore {
	for i := len(rec.Rounds) - 1; i >= 0; i-- {
		rd := rec.Rounds[i]
		if rd.Screen == nil {
			continue
		}
		if rd.Decision == DecisionCommitted {
			return &rd.Screen.B
		}
		return &rd.Screen.A
	}
	return &rec.Baseline.Dev
}

// extend runs the second look of a done run with more budget (A2.11): more
// trials of version 0 and the final version on the old held-out tasks, plus
// benchmark tasks the run never used, tested at the reserved alpha.
func (r *Runner) extend(ctx context.Context, id string, extraTrials int, newTasks bool, extraUSD float64) (*Record, error) {
	ctx, release, err := r.acquire(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	v := rec.Verdict
	switch {
	case rec.Status != StatusDone || v == nil || rec.Final == nil:
		return rec, fmt.Errorf("only a finished run with a held-out result can be continued: %w", ErrState)
	case v.State != VerdictLikelyBetter && v.State != VerdictInsufficientSample:
		return rec, fmt.Errorf("a run whose result is %s cannot be continued: %w", v.State, ErrState)
	case !rec.Spec.Claim.Extension:
		return rec, fmt.Errorf("this run kept no reserve for a second look: %w", ErrState)
	case rec.Extension != nil:
		return rec, fmt.Errorf("this run already had its second look: %w", ErrState)
	case extraTrials < 0 || extraTrials > MaxTrials || extraUSD < 0:
		return rec, fmt.Errorf("extra tries must be 0 to %d and the extra budget must not be negative: %w", MaxTrials, ErrState)
	case extraTrials == 0 && !newTasks:
		return rec, fmt.Errorf("a second look needs more tries or new tasks: %w", ErrState)
	}
	defer func() { r.event(rec) }()
	first := v
	rec.Revision++
	if rec.Spec.Budget.TotalUSD > 0 { // 0 means no limit
		rec.Spec.Budget.TotalUSD += extraUSD
	}
	rec.Status, rec.Reason, rec.Error = StatusRunning, "running the second look", ""
	if err := r.saveLive(rec); err != nil {
		return rec, err
	}
	runs, err := r.loadRuns(id, FinalRunsFile)
	if err != nil {
		return r.extendFailed(rec, first, err)
	}
	arms := []Version{rec.Base, rec.Incumbent}
	added := []RunScore{}
	fc := &FinalClaim{Arms: []string{rec.Base.Ref, rec.Incumbent.Ref}, Trials: rec.Spec.Sealed.Trials + extraTrials}
	score := func(round int, tasks []string, trials int) error {
		if err := r.hardStop(rec, "the second look", scoreCost(rec, 2, len(tasks), trials)); err != nil {
			return err
		}
		res, err := r.Exec.Score(ctx, ScoreRequest{ImprovementID: rec.ID, Spec: &rec.Spec, Round: round, Phase: PhaseExtension,
			Arms: arms, Tasks: tasks, Trials: trials})
		if err != nil {
			return fmt.Errorf("second look on held-out tasks: %w", err)
		}
		rec.Controls.CostUSD += res.CostUSD
		fc.ExpID = res.ExpID
		for _, x := range res.Runs {
			x.Trial += 100 * round // keep trial numbers apart from the final experiment's
			added = append(added, x)
		}
		return nil
	}
	if rec.Controls == nil {
		rec.Controls = &Controls{}
	}
	if extraTrials > 0 {
		if err := score(1, rec.Splits.Sealed, extraTrials); err != nil {
			return r.extendFailed(rec, first, err)
		}
	}
	tasks := slices.Clone(rec.Splits.Sealed)
	if newTasks {
		fresh, err := r.freshTasks(ctx, rec)
		if err != nil {
			return r.extendFailed(rec, first, err)
		}
		if len(fresh) > 0 {
			if err := score(2, fresh, rec.Spec.Sealed.Trials+extraTrials); err != nil {
				return r.extendFailed(rec, first, err)
			}
			tasks = append(tasks, fresh...)
		}
	}
	if err := r.saveRuns(id, ExtensionRunsFile, added); err != nil {
		return r.extendFailed(rec, first, err)
	}
	in := r.claimInput(rec, slices.Concat(runs, added), tasks)
	in.Level = rec.Spec.Claim.ExtensionAlpha
	if in.Level <= 0 {
		in.Level = DefaultExtensionAlpha
	}
	in.Trials = fc.Trials
	nv := r.claimRule().Decide(in)
	fillFinal(fc, nv)
	rec.Extension = fc
	r.decorate(rec, nv)
	nv.Notes = append([]string{"First look: " + first.Headline}, nv.Notes...)
	rec.Verdict = nv
	rec.Status, rec.Reason = StatusDone, nv.Headline
	return rec, r.saveLive(rec)
}

// extendFailed puts a run whose second look failed back to its first
// verdict, so the second look can be tried again.
func (r *Runner) extendFailed(rec *Record, first *Verdict, err error) (*Record, error) {
	var hs *hardStopError
	rec.Verdict, rec.Status, rec.Reason = first, StatusDone, first.Headline
	if errors.As(err, &hs) {
		rec.Reason = hs.msg
	} else {
		rec.Error = err.Error()
	}
	return rec, errors.Join(err, r.saveLive(rec))
}

// freshTasks are benchmark tasks the run never used: not in any split, not
// honeypots, not impossible, at most maxNewTasks, in id order.
func (r *Runner) freshTasks(ctx context.Context, rec *Record) ([]string, error) {
	all := rec.Spec
	all.Tasks = nil
	infos, err := r.Exec.Tasks(ctx, &all)
	if err != nil {
		return nil, fmt.Errorf("list tasks of %s: %w", rec.Spec.Suite, err)
	}
	used := slices.Concat(rec.Splits.Dev, rec.Splits.Audit, rec.Splits.Sealed, rec.Splits.Honeypots)
	out := []string{}
	for _, t := range infos {
		if !t.Impossible && !slices.Contains(used, t.ID) {
			out = append(out, t.ID)
		}
	}
	slices.Sort(out)
	if len(out) > maxNewTasks {
		out = out[:maxNewTasks]
	}
	return out, nil
}

func (r *Runner) saveRuns(id, name string, runs []RunScore) error {
	b, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(r.dir(id), name), b, 0o600)
}

func (r *Runner) loadRuns(id, name string) ([]RunScore, error) {
	b, err := os.ReadFile(filepath.Join(r.dir(id), name))
	if err != nil {
		return nil, fmt.Errorf("read the final check's runs: %w", err)
	}
	var runs []RunScore
	if err := json.Unmarshal(b, &runs); err != nil {
		return nil, fmt.Errorf("read the final check's runs: %w", err)
	}
	return runs, nil
}
