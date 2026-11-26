package improve

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/stats"
)

// ContinueRequest is the body of POST .../continue and .../continue/estimate
// (A2.11): more tries on the held-out tasks, new held-out tasks, and the
// money the spending limit rises by.
type ContinueRequest struct {
	ExtraTrials    int     `json:"extra_trials"`
	NewTasks       bool    `json:"new_tasks"`
	ExtraBudgetUSD float64 `json:"extra_budget_usd,omitempty"`
}

// MaxContinueNewTasks caps the new held-out tasks of a second look.
const MaxContinueNewTasks = 200

// continueRefusal is a plain refusal of a continue; it reads as ErrState.
type continueRefusal string

func (e continueRefusal) Error() string { return string(e) }
func (e continueRefusal) Unwrap() error { return ErrState }

// CanContinue reports whether a second look is offered (A2.11): the state
// is likely better or not enough tasks, the reserve was kept at creation, no
// second look ran yet, and the run is done. The error says why not.
func CanContinue(rec *Record) error {
	switch {
	case rec.InStudy:
		return continueRefusal("A loop of a study cannot be continued on its own.")
	case rec.Extension != nil || rec.Revision > 0:
		return continueRefusal("This run already had its second look; it cannot be continued again.")
	case rec.Status != StatusDone:
		return continueRefusal(fmt.Sprintf("This run is %s; only a finished run can be continued.", rec.Status))
	case rec.Final == nil:
		return continueRefusal("This run has no held-out result, so there is nothing to look at again.")
	case !rec.Spec.Claim.Extension:
		return continueRefusal("This run kept no reserve for a second look.")
	case rec.Verdict == nil || (rec.Verdict.State != VerdictLikelyBetter && rec.Verdict.State != VerdictInsufficientSample):
		return continueRefusal("A second look is offered only when the result is likely better or there were not enough held-out tasks.")
	}
	return nil
}

// finalSD is the spread of the final experiment's per-task differences,
// read back from its 80% interval.
func finalSD(f *FinalClaim) float64 {
	if f == nil || f.N < 2 {
		return claimSDFloor
	}
	half := (f.CI80[1] - f.CI80[0]) / 2
	t := stats.TQuantile(0.90, f.N-1)
	if !(half > 0) || !(t > 0) {
		return claimSDFloor
	}
	return math.Max(half*math.Sqrt(float64(f.N))/t, claimSDFloor)
}

func extensionAlpha(s *Spec) float64 {
	if s.Claim.ExtensionAlpha > 0 {
		return s.Claim.ExtensionAlpha
	}
	return DefaultExtensionAlpha
}

func targetGain(s *Spec) float64 {
	if s.Claim.TargetGain > 0 {
		return s.Claim.TargetGain
	}
	return DefaultTargetGain
}

// TasksNeededForTarget is n_needed of A2.11: the held-out tasks a test at
// the extension level needs to confirm the target gain with 80% power.
func TasksNeededForTarget(rec *Record) int {
	z := stats.TQuantile(1-extensionAlpha(&rec.Spec), 100000) + stats.TQuantile(0.80, 100000)
	x := z * finalSD(rec.Final) / targetGain(&rec.Spec)
	return int(math.Ceil(x * x))
}

// ContinueOfferFor is the second look a done run may buy: in's extra tries
// on the old held-out tasks plus, when asked, up to newTasks new ones.
// perRunUSD is the price of one run (0 when unknown).
func ContinueOfferFor(rec *Record, in ContinueRequest, newTasks int, perRunUSD float64) *ContinueOffer {
	n := 0
	if rec.Final != nil {
		n = rec.Final.N
	}
	if !rec.Spec.Claim.Extension {
		return &ContinueOffer{Text: fmt.Sprintf("This run kept no reserve for a second look. Start a new run with the suggested size: %d held-out tasks.",
			TasksNeededForTarget(rec))}
	}
	add := 0
	if in.NewTasks {
		add = min(max(newTasks, 0), MaxContinueNewTasks)
	}
	trials := rec.Spec.Sealed.Trials + in.ExtraTrials
	runs := 2 * (n*max(in.ExtraTrials, 0) + add*max(trials, 0))
	o := &ContinueOffer{ExtraTrials: in.ExtraTrials, NewTasks: add, Runs: runs}
	if perRunUSD > 0 {
		o.USD = [2]float64{0.8 * perRunUSD * float64(runs), 1.25 * perRunUSD * float64(runs)}
	}
	if total := n + add; total >= 2 {
		level := extensionAlpha(&rec.Spec)
		o.CanConfirm = 100 * (stats.TQuantile(1-level, total-1) + stats.TQuantile(0.80, total-1)) * finalSD(rec.Final) / math.Sqrt(float64(total))
	}
	switch {
	case runs == 0:
		o.Text = "This second look adds no runs: ask for more tries or new held-out tasks."
	case in.NewTasks && add == 0 && in.ExtraTrials == 0:
		o.Text = "The benchmark has no tasks left that the run has not used."
	case in.ExtraTrials > 0 && add == 0:
		o.Text = fmt.Sprintf("%d more runs on the same %d held-out tasks. More tries on the same tasks cannot confirm a gain this size; more tasks can.", runs, n)
		if o.CanConfirm > 0 && o.CanConfirm <= 100*targetGain(&rec.Spec) {
			o.Text = fmt.Sprintf("%d more runs on the same %d held-out tasks.", runs, n)
		}
	default:
		o.Text = fmt.Sprintf("%d more runs on %d held-out tasks, %d of them new.", runs, n+add, add)
	}
	if o.CanConfirm > 0 {
		o.Text += fmt.Sprintf(" With them, gains of about %s points or more can be confirmed.", unsignedPoints(o.CanConfirm/100))
	}
	return o
}

// ErrNoExtend is the answer of a build whose runner cannot run a second
// look (RunExtend is nil).
var ErrNoExtend = errors.New("This build cannot continue improvement runs.")

// CheckContinue loads a run and says whether it may be continued now.
func (r *Runner) CheckContinue(id string) (*Record, error) {
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if r.Running(id) {
		return rec, continueRefusal("This run is already running.")
	}
	if err := CanContinue(rec); err != nil {
		return rec, err
	}
	if RunExtend == nil {
		return rec, ErrNoExtend
	}
	return rec, nil
}

// OfferContinue prices a second look exactly as RunExtend runs it: the same
// new tasks (every benchmark task the run never used, at most
// MaxContinueNewTasks), the same tries and the hard stop's cost per run.
// A run with no reserve gets the "start a new run" offer.
func (r *Runner) OfferContinue(ctx context.Context, id string, in ContinueRequest) (*ContinueOffer, error) {
	rec, err := r.load(id)
	if err != nil {
		return nil, err
	}
	if err := CanContinue(rec); err != nil && rec.Spec.Claim.Extension {
		return nil, err
	}
	fresh := 0
	if in.NewTasks && rec.Spec.Claim.Extension && r.Exec != nil {
		f, err := r.freshTasks(ctx, rec)
		if err != nil {
			return nil, err
		}
		fresh = len(f)
	}
	return ContinueOfferFor(rec, in, fresh, perRunUSD(rec)), nil
}
