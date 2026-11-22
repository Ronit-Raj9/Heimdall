package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/forecast"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

const estimateUsage = `usage: bifrost-bench estimate [-config deploy/bench.yaml] [-json] [-allow-unpriced] experiments/<file>.yaml
Plans the experiment as plan does and forecasts its cost and time from the sealed history of the same agent configs on the same suite. Nothing is frozen and nothing runs.`

func init() {
	register("estimate", "forecast an experiment's cost and time from sealed history; nothing is frozen and nothing runs", runEstimate)
}

func runEstimate(args []string) int {
	fs := flag.NewFlagSet("estimate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	asJSON := fs.Bool("json", false, "print the forecast as JSON")
	allowUnpriced := fs.Bool("allow-unpriced", false, "plan matrix models the price snapshot does not list")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, estimateUsage)
		return exitUsage
	}
	ctx := context.Background()
	// The planner runs the gateway preflight, as plan does.
	a, err := newApp(ctx, *config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	exp, err := loadExperiment(fs.Arg(0))
	if err != nil {
		return fail("%v", err)
	}
	allowUnpricedModels(exp, *allowUnpriced)
	if err := a.validatePolicy(exp); err != nil {
		return fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fail("plan: %v", err)
	}
	f, err := a.forecast(ctx, m)
	if err != nil {
		return fail("estimate: %v", err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(f); err != nil {
			return fail("estimate: %v", err)
		}
		return exitOK
	}
	printPlan(m)
	printForecast(os.Stdout, f)
	return exitOK
}

// allowUnpricedModels records -allow-unpriced on the experiment's matrix,
// so the frozen manifest says unpriced models were allowed on purpose. An
// experiment without a matrix is left as it is.
func allowUnpricedModels(exp *schemas.Experiment, allow bool) {
	if allow && exp.Matrix != nil {
		mx := *exp.Matrix
		mx.AllowUnpriced = true
		exp.Matrix = &mx
	}
}

// priced reports whether the price snapshot lists model. The planner uses
// it to refuse unpriced matrix models (engine.WithModelPrices).
func (a *app) priced(model string) bool {
	_, ok := a.prices.Lookup(model)
	return ok
}

// forecast reads the sealed history of the manifest's suite through the
// store's verified index (cached for historyCacheTTL, cachedHistory) and
// forecasts the manifest.
func (a *app) forecast(ctx context.Context, m *schemas.Manifest) (*schemas.Forecast, error) {
	start := time.Now()
	h, hit, err := a.cachedHistory(ctx, a.store, m, false)
	if err != nil {
		return nil, err
	}
	library, libraryHit := a.cachedLibraryRuns(ctx, m)
	concurrency := m.Experiment.Concurrency
	if concurrency <= 0 {
		concurrency = engine.DefaultConcurrency
	}
	f := forecast.Estimate(forecast.Input{
		Manifest: m, History: h.Runs, Priced: a.priced, PriceRef: a.prices.Ref(), Concurrency: concurrency, Notes: h.Notes(), Now: time.Now(),
		Library: library, Reprice: a.reprice,
	})
	a.logger.Debug("forecast", "experiment", m.ExpID, "suite", m.SuiteID+"@"+m.SuiteVersion,
		"history_cache", hitText(hit), "library_cache", hitText(libraryHit), "took_ms", time.Since(start).Milliseconds())
	return f, nil
}

// historyCacheTTL is how long the sealed history of a suite is served from
// historyCache; a run sealed in the store changes its fingerprint and ends
// the entry sooner.
const historyCacheTTL = 60 * time.Second

// historyCache holds the sealed history per store and suite version for the
// estimates the evaluation wizard asks for on every change (D4.slow).
var historyCache = forecast.NewHistoryCache(historyCacheTTL)

// cachedHistory is forecast.LoadHistory (or LoadLibrary when library) of m
// in st through historyCache; hit says the store was not scanned again. A
// store with no folder on disk is read directly.
func (a *app) cachedHistory(ctx context.Context, st forecast.Store, m *schemas.Manifest, library bool) (*forecast.History, bool, error) {
	key, fp := storeFingerprint(ctx, st)
	if fp == "" {
		if library {
			h, err := forecast.LoadLibrary(ctx, st, m, sealedRunFile)
			return h, false, err
		}
		h, err := forecast.LoadHistory(ctx, st, m, sealedRunFile)
		return h, false, err
	}
	s, hit, err := historyCache.Suite(ctx, st, key, fp, m, sealedRunFile)
	if err != nil {
		return nil, false, err
	}
	var h *forecast.History
	if library {
		h, err = s.Library(m)
	} else {
		h, err = s.History(m)
	}
	return h, hit && err == nil, err
}

// cachedLibraryRuns is libraryRuns (forecastlibrary.go) through
// historyCache: every sealed run on m's suite version in the org's store
// and the operator's library store. hit is true when no store was scanned.
func (a *app) cachedLibraryRuns(ctx context.Context, m *schemas.Manifest) ([]forecast.HistoryRun, bool) {
	var out []forecast.HistoryRun
	hit := true
	for _, st := range []forecast.Store{a.store, a.operatorLibrary()} {
		if st == nil {
			continue
		}
		h, ok, err := a.cachedHistory(ctx, st, m, true)
		if err != nil {
			a.logger.Warn("library runs not read", "experiment", m.ExpID, "error", err.Error())
			continue
		}
		hit = hit && ok
		out = append(out, h.Runs...)
	}
	return out, hit
}

// storeFingerprint names a run store on disk (its root) and stamps what it
// holds: the number of experiments and, through the size and time of each
// experiment's seals.log (one line per sealed run), the sealed runs. Both
// are "" for a store with no root or one that cannot be listed (no caching).
func storeFingerprint(ctx context.Context, st forecast.Store) (key, fingerprint string) {
	r, ok := st.(interface{ Root() string })
	if !ok || r.Root() == "" {
		return "", ""
	}
	root := r.Root()
	ids, err := st.ListExperiments(ctx)
	if err != nil {
		return "", ""
	}
	var size, latest int64
	for _, id := range ids {
		info, err := os.Stat(filepath.Join(root, id, "seals.log")) // store.sealsLogFile
		if err != nil {
			continue // no run sealed yet
		}
		size += info.Size()
		latest = max(latest, info.ModTime().UnixNano())
	}
	return root, fmt.Sprintf("%d/%d/%d", len(ids), size, latest)
}

// hitText is "hit" or "miss" for the debug log.
func hitText(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

// printEstimate prints the forecast under plan's slot counts. A forecast
// that cannot be read only warns: plan's answer is the slots.
func (a *app) printEstimate(ctx context.Context, m *schemas.Manifest) {
	f, err := a.forecast(ctx, m)
	if err != nil {
		a.logger.Warn("no forecast", "experiment", m.ExpID, "error", err)
		return
	}
	printForecast(os.Stdout, f)
}

// sealedRunFile reads one file of a sealed run's folder; the forecast reads
// proxy.ledger.jsonl to recognise runs against the scripted model.
func sealedRunFile(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
	if sr.Dir == "" || strings.ContainsAny(name, `/\`) {
		return nil, false, nil
	}
	data, err := os.ReadFile(filepath.Join(sr.Dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// printForecast writes the forecast as plain lines: per arm, the total, the
// judge, the budget fit, the basis and the notes.
func printForecast(w io.Writer, f *schemas.Forecast) {
	fmt.Fprintf(w, "forecast  %s\n", f.Basis)
	for _, a := range f.Arms {
		fmt.Fprintf(w, "  arm %-6s %-28s %3d runs  cost %s  agent time %s\n", a.Arm, truncate(a.Label, 28), a.Runs,
			rangeText(a.CostUSD, moneyText), rangeText(a.AgentTimeS, durationText))
		fmt.Fprintf(w, "             basis %s: %s\n", a.Basis, a.BasisText)
	}
	fmt.Fprintf(w, "  total      cost %s  agent time %s  wall clock %s (concurrency %d)\n",
		rangeText(f.CostUSD, moneyText), rangeText(f.AgentTimeS, durationText), rangeText(f.WallClockS, durationText), f.Concurrency)
	if j := f.Judge; j != nil {
		fmt.Fprintf(w, "  judge      cost %s over %d runs, apart from the agent cost: %s\n", rangeText(j.CostUSD, moneyText), j.Runs, j.BasisText)
	}
	budget := "no budget"
	if f.BudgetUSD > 0 {
		budget = "budget " + moneyText(f.BudgetUSD)
	}
	fmt.Fprintf(w, "  %s: %s (%s)\n", budget, strings.ReplaceAll(f.Fit, "_", " "), f.FitReason)
	for _, n := range f.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
}

// rangeText writes a forecast range: "low to high (median m)", "at most
// high", or the reason it is unknown.
func rangeText(r schemas.ForecastRange, format func(float64) string) string {
	switch r.Kind {
	case schemas.RangeHistory:
		return fmt.Sprintf("%s to %s (median %s)", format(*r.Low), format(*r.High), format(*r.Median))
	case schemas.RangeUpperBound:
		return "at most " + format(*r.High)
	default:
		return "unknown: " + r.Reason
	}
}

// moneyText writes dollars with cents, or four decimals below a dollar.
func moneyText(v float64) string {
	if math.Abs(v) < 1 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// durationText writes seconds as a rounded duration, e.g. 6m10s or 2h5m.
func durationText(s float64) string {
	d := time.Duration(s * float64(time.Second))
	switch {
	case d >= time.Hour:
		d = d.Round(time.Minute)
	default:
		d = d.Round(time.Second)
	}
	out := d.String()
	if strings.HasSuffix(out, "m0s") {
		out = strings.TrimSuffix(out, "0s")
	}
	if strings.HasSuffix(out, "h0m") {
		out = strings.TrimSuffix(out, "0m")
	}
	return out
}

// truncate shortens s to n runes with a trailing "...".
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}

// serveForecast is the server's handlers.ForecastSource: the same planner,
// history and matrix code as the estimate and matrix commands.
type serveForecast struct {
	a *app
}

var _ handlers.ForecastSource = serveForecast{}

// Estimate implements handlers.ForecastSource. A planner error is a refusal.
func (s serveForecast) Estimate(ctx context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	if err := s.a.validatePolicy(exp); err != nil {
		return nil, fmt.Errorf("%w: %w", handlers.ErrPlanRefused, err)
	}
	m, err := s.a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", handlers.ErrPlanRefused, err)
	}
	return s.a.forecast(ctx, m)
}

// Priced reports whether the price snapshot lists model; the evaluation
// estimate (handlers/evaluationestimate.go) leaves a cost unknown without it.
func (s serveForecast) Priced(model string) bool { return s.a.priced(model) }

// Matrix implements handlers.ForecastSource.
func (s serveForecast) Matrix(ctx context.Context, expID string, q leaderboard.MatrixQuery) (*schemas.ModelMatrix, error) {
	return s.a.matrix(ctx, expID, q)
}
