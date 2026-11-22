package handlers

// Honest budgets for evaluations (G4-01). Every benchmark of an evaluation
// runs under a spend cap: its share of the evaluation's budget_usd, or
// EvaluationDefaultBenchmarkBudget when the spec sets none. The cap is never
// hidden: the estimate says which cap applies, suggests one that covers the
// expected cost (capped by the org-level maximum), and says how many runs a
// smaller cap would leave unstarted; the evaluation shows each benchmark's
// spend, cap and the runs the cap stopped; POST /evaluations/{id}/raise-budget
// raises the caps and continues the stopped benchmarks from their sealed runs.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Budget defaults.
const (
	// EvaluationDefaultBenchmarkBudget is the cap of each benchmark when the
	// spec sets no budget_usd (the API default; the wizard always sends the
	// budget it shows).
	EvaluationDefaultBenchmarkBudget = QuickRunDefaultBudget
	// EvaluationSuggestHeadroom is how far above the high estimate the
	// suggested budget goes, so a run that costs a little more than
	// expected does not stop the evaluation.
	EvaluationSuggestHeadroom = 1.25
	// EvaluationBudgetMaxEnv sets the org-level maximum of the suggested
	// budget, in USD; EvaluationBudgetMaxDefault applies without it. A
	// person can still type a larger budget, up to EvaluationMaxBudget.
	EvaluationBudgetMaxEnv     = "HEIMDALL_EVAL_BUDGET_MAX_USD"
	EvaluationBudgetMaxDefault = 100.0
)

// EvalBudget is the budget part of the estimate: which cap applies, the
// suggested default, and what a cap below the expected cost would do.
type EvalBudget struct {
	// BudgetUSD is the evaluation's cap and PerBenchmarkUSD each benchmark's
	// share; with no budget_usd in the request they are the defaults.
	BudgetUSD       float64 `json:"budget_usd"`
	PerBenchmarkUSD float64 `json:"per_benchmark_usd"`
	Default         bool    `json:"default"` // no budget_usd was given: the per-benchmark default applies
	// DefaultPerBenchmarkUSD is the per-benchmark cap with no budget_usd.
	DefaultPerBenchmarkUSD float64 `json:"default_per_benchmark_usd"`
	// SuggestedUSD covers the high cost estimate with headroom, at least the
	// default, at most MaxUSD; the wizard starts the budget box from it.
	SuggestedUSD float64 `json:"suggested_usd"`
	// MaxUSD is the org-level maximum of the suggestion (the server's
	// HEIMDALL_EVAL_BUDGET_MAX_USD, lowered to the org's daily spend cap
	// when a server admin set one).
	MaxUSD float64 `json:"max_usd"`
	// Fit compares the cap with the cost estimate: fits, may_exceed (the low
	// estimate fits, the high one does not), exceeds, or unknown.
	Fit string `json:"fit"`
	// RunsSkippedLow and RunsSkippedHigh are how many runs the cap would
	// leave unstarted at the low and at the high cost estimate.
	RunsSkippedLow  int    `json:"runs_skipped_low"`
	RunsSkippedHigh int    `json:"runs_skipped_high"`
	Note            string `json:"note,omitempty"`
}

// Budget fits.
const (
	BudgetFits      = "fits"
	BudgetMayExceed = "may_exceed"
	BudgetExceeds   = "exceeds"
	BudgetUnknown   = "unknown"
)

// RaiseBudgetRequest is the body of POST /evaluations/{id}/raise-budget.
type RaiseBudgetRequest struct {
	// BudgetUSD is the new budget of the whole evaluation; each benchmark
	// gets an equal share of it, never less than it has already spent.
	BudgetUSD float64 `json:"budget_usd"`
}

func init() {
	RegisterOrgArea("evaluationbudget", func(env OrgEnv, r *router.Router) {
		h := &evaluationsHandler{env: env, now: time.Now}
		r.POST(APIPrefix+"/evaluations/{id}/raise-budget", env.W(h.raiseBudget))
	})
}

// evalConcurrency is the runs of one benchmark that go at once.
func evalConcurrency(spec schemas.EvaluationSpec) int {
	if spec.Concurrency > 0 {
		return spec.Concurrency
	}
	return EvaluationDefaultConcurrency
}

// budgetMax is the org-level maximum of a suggested budget: the server's
// setting, at most EvaluationMaxBudget, lowered to dailyCap when a server
// admin set one (0: none).
func budgetMax(dailyCap float64) float64 {
	m := EvaluationBudgetMaxDefault
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(EvaluationBudgetMaxEnv)), 64); err == nil && v > 0 {
		m = v
	}
	m = min(m, EvaluationMaxBudget)
	if dailyCap > 0 {
		m = min(m, dailyCap)
	}
	return m
}

// estimateBudget fills the budget part of an estimate. Per benchmark that
// runs, the runs left unstarted are the runs past the share of the cap at
// the per-run low and high cost; a benchmark with no known cost counts as
// unknown.
func estimateBudget(est *EvalEstimate, requested, dailyCap float64) EvalBudget {
	b := EvalBudget{DefaultPerBenchmarkUSD: EvaluationDefaultBenchmarkBudget, MaxUSD: budgetMax(dailyCap), Fit: BudgetUnknown}
	n := 0
	for _, be := range est.PerBenchmark {
		if be.Skipped == "" {
			n++
		}
	}
	if n == 0 {
		return b
	}
	if requested > 0 {
		b.BudgetUSD, b.PerBenchmarkUSD = requested, cents(requested/float64(n))
	} else {
		b.Default, b.PerBenchmarkUSD = true, EvaluationDefaultBenchmarkBudget
		b.BudgetUSD = EvaluationDefaultBenchmarkBudget * float64(n)
	}
	floor := EvaluationDefaultBenchmarkBudget * float64(n)
	b.SuggestedUSD = min(floor, b.MaxUSD)
	known := true
	for _, be := range est.PerBenchmark {
		if be.Skipped != "" || be.Runs == 0 {
			continue
		}
		if !be.EstCostUSD.Known {
			known = false
			continue
		}
		b.RunsSkippedLow += runsPast(be.Runs, be.EstCostUSD.Low, b.PerBenchmarkUSD)
		b.RunsSkippedHigh += runsPast(be.Runs, be.EstCostUSD.High, b.PerBenchmarkUSD)
	}
	if known && est.EstCostUSD.Known {
		want := math.Ceil(est.EstCostUSD.High * EvaluationSuggestHeadroom) // whole dollars
		b.SuggestedUSD = min(max(want, floor), b.MaxUSD)
		switch {
		case b.RunsSkippedHigh == 0:
			b.Fit = BudgetFits
		case b.RunsSkippedLow == 0:
			b.Fit = BudgetMayExceed
		default:
			b.Fit = BudgetExceeds
		}
		if want > b.MaxUSD {
			b.Note = fmt.Sprintf("the expected cost is above this organization's maximum suggested budget of %s; type a larger budget to run every task", usd(b.MaxUSD))
		}
	} else {
		b.Note = fmt.Sprintf("the cost is not known yet, so the suggestion is the default of %s per benchmark", usd(EvaluationDefaultBenchmarkBudget))
	}
	return b
}

// runsPast is how many of runs would not start under a cap of capUSD when
// all of them cost total together, spread evenly.
func runsPast(runs int, total, capUSD float64) int {
	if runs <= 0 || total <= capUSD || total <= 0 {
		return 0
	}
	afford := int(math.Floor(capUSD / (total / float64(runs))))
	return max(0, runs-afford)
}

// budgetFacts records a child's cap, spend and the runs the cap stopped
// from its live status; it reports whether anything changed. The cap is
// the child's own (raised in place by raiseBudget), else its manifest's.
// A child is budget stopped when it ended with runs never started while its
// spend reached the cap less one average run: the scheduler leases a slot
// only while spend plus its reservation stays under the cap.
func (h *evaluationsHandler) budgetFacts(c *schemas.EvaluationChild, m *schemas.Manifest, st schemas.ExperimentStatus) bool {
	before := *c
	capUSD := c.BudgetUSD
	if capUSD <= 0 && m != nil {
		capUSD = m.Experiment.Budget.TotalUSD
	}
	c.BudgetUSD = capUSD
	spent, _ := st.SpentUSD.Float()
	c.SpentUSD = cents(spent)
	notStarted := max(0, st.Slots.Total-st.Slots.Done-st.Slots.Failed-st.Slots.Running)
	ran := st.Slots.Done + st.Slots.Failed
	avg := 0.0
	if ran > 0 {
		avg = spent / float64(ran)
	}
	c.BudgetStopped = st.State == schemas.ExperimentFailed && notStarted > 0 && capUSD > 0 && spent+avg >= capUSD
	c.NotStarted = 0
	if c.BudgetStopped {
		c.NotStarted = notStarted
	}
	return before.BudgetUSD != c.BudgetUSD || before.SpentUSD != c.SpentUSD || before.BudgetStopped != c.BudgetStopped || before.NotStarted != c.NotStarted
}

// raiseBudget handles POST /evaluations/{id}/raise-budget: every benchmark
// gets an equal share of the new budget (never below what it spent), the
// live cap of each one still running is raised in place, and each one the
// cap stopped is continued from its sealed runs. 202 with the evaluation.
func (h *evaluationsHandler) raiseBudget(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	rc, _ := h.env.Services.Launcher.(RunController)
	if rc == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "continuing an evaluation needs the job runner on this server")
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	var req RaiseBudgetRequest
	if !decodeOptional(ctx, h.env.Mode, &req, `{"budget_usd"}`) {
		return
	}
	h.refresh(ev)
	kids := budgetChildren(ev)
	if len(kids) == 0 {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" has no benchmark left to run")
		return
	}
	current := 0.0
	for _, c := range kids {
		current += c.BudgetUSD
	}
	if !(req.BudgetUSD > current) || req.BudgetUSD > EvaluationMaxBudget {
		h.fail(ctx, fasthttp.StatusBadRequest, fmt.Sprintf("budget_usd must be more than the current %s and at most %s", usd(current), usd(EvaluationMaxBudget)))
		return
	}
	share := cents(req.BudgetUSD / float64(len(kids)))
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	wctx := withPrincipal(work, ctx)
	var errs []string
	for _, c := range kids {
		next := max(share, cents(c.SpentUSD+0.01))
		if _, err := op.PatchControls(wctx, c.ExperimentID, runctl.Patch{TotalUSD: &next}); err != nil {
			errs = append(errs, c.Benchmark+": "+err.Error())
			continue
		}
		c.BudgetUSD = next
		if !c.BudgetStopped {
			continue // still running: the live cap applies at its next lease
		}
		if err := rc.Resume(wctx, c.ExperimentID); err != nil && !errors.Is(err, schemas.ErrExists) {
			errs = append(errs, c.Benchmark+": "+err.Error())
			continue
		}
		c.BudgetStopped, c.NotStarted, c.Status = false, 0, string(schemas.ExperimentRunning)
	}
	ev.Spec.BudgetUSD = cents(req.BudgetUSD)
	ev.Status = evaluationStatus(&schemas.Evaluation{Status: schemas.EvaluationRunning, Children: ev.Children})
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	if len(errs) > 0 {
		h.fail(ctx, fasthttp.StatusBadGateway, "some benchmarks did not continue: "+strings.Join(errs, "; "))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ev)
}

// budgetChildren are the children a budget raise applies to: launched,
// and either still running or stopped by the cap.
func budgetChildren(ev *schemas.Evaluation) []*schemas.EvaluationChild {
	var out []*schemas.EvaluationChild
	for i := range ev.Children {
		c := &ev.Children[i]
		if c.ExperimentID == "" || c.Status == schemas.EvaluationChildSkipped {
			continue
		}
		if c.BudgetStopped || !terminalChild(c.Status) {
			out = append(out, c)
		}
	}
	return out
}

// cents rounds down to whole cents.
func cents(v float64) float64 { return math.Floor(v*100+1e-9) / 100 }

// usd writes dollars with cents.
func usd(v float64) string { return fmt.Sprintf("$%.2f", v) }
