package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

const matrixUsage = `usage: bifrost-bench matrix [-config deploy/bench.yaml] -exp <id> [-condition <id>] [-grade-source original|<regrade id>] [-k n] [-alpha 0.05] [-split public|held_out] [-json]
Compares every arm of one experiment from its sealed runs: verified success with its interval, pass^k, cost per success, tokens, wall time and false completion per arm, and every pair by the exact sign-flip test, Holm-adjusted across all pairs. Reads the run folder only; no gateway is needed.`

func init() {
	register("matrix", "compare every arm of one experiment: success, pass^k, cost per success, pairwise p values with Holm", runMatrix)
}

func runMatrix(args []string) int {
	fs := flag.NewFlagSet("matrix", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "experiment id")
	condition := fs.String("condition", "", "condition (default: the experiment's primary condition)")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, or a regrade id of the experiment")
	k := fs.Int("k", 0, "k of pass^k for every arm, 2 or more (default: min(3, trials per task))")
	alpha := fs.Float64("alpha", leaderboard.DefaultAlpha, "significance level for the pairwise tests")
	split := fs.String("split", "", "public (default) or held_out: the frozen split of tasks the matrix reads")
	asJSON := fs.Bool("json", false, "print the matrix as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *expID == "" {
		fmt.Fprintln(os.Stderr, matrixUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	mx, err := a.matrix(ctx, *expID, leaderboard.MatrixQuery{Condition: *condition, GradeSource: *gradeSource, K: *k, Alpha: *alpha, Split: *split})
	if errors.Is(err, leaderboard.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, matrixUsage)
		return exitUsage
	}
	if err != nil {
		return fail("matrix %s: %v", *expID, err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(mx); err != nil {
			return fail("matrix %s: %v", *expID, err)
		}
		return exitOK
	}
	printMatrix(os.Stdout, mx)
	return exitOK
}

// matrix computes one experiment's model comparison matrix from its sealed
// runs, read through the store's verified index with the grade source.
func (a *app) matrix(ctx context.Context, expID string, q leaderboard.MatrixQuery) (*schemas.ModelMatrix, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	runs, err := a.store.SealedRuns(ctx, expID, q.GradeSource)
	if err != nil {
		return nil, err
	}
	synthetic, err := leaderboard.SyntheticArms(ctx, m, runs, sealedRunFile)
	if err != nil {
		return nil, err
	}
	synth := false
	for _, s := range synthetic {
		synth = synth || s
	}
	// The matrix takes the board's experiment eligibility: an experiment
	// whose newest verdict is INVALID ranks nothing.
	if d, ok := (indexAdapter{root: a.store.Root()}).LatestDecision(expID); ok && d == schemas.DecisionInvalid {
		q.Invalid = true
	}
	return leaderboard.Matrix(m, runs, synth, q)
}

// printMatrix writes the matrix table, the pairwise grid and the note.
func printMatrix(w io.Writer, mx *schemas.ModelMatrix) {
	fmt.Fprintf(w, "matrix %s  suite %s  condition %s  grade source %s  (conditions: %s)\n",
		mx.ExpID, mx.Suite, mx.Condition, mx.GradeSource, strings.Join(mx.Conditions, ", "))
	if mx.Synthetic {
		fmt.Fprintln(w, "synthetic: scripted agent, scripted model or smoke experiment; the numbers are not findings")
	}
	if mx.Invalid {
		fmt.Fprintln(w, "INVALID: the experiment's newest verdict is invalid; nothing is ranked")
	}
	if mx.Split != "" {
		fmt.Fprintf(w, "split: %s tasks only\n", mx.Split)
	}
	fmt.Fprintf(w, "%-4s %-4s %-4s %-28s %-22s %-20s %-10s %-12s %-10s %-9s %s\n",
		"rank", "tier", "arm", "label", "model", "success [95%]", "pass^k", "cost/succ", "tokens", "wall s", "false compl.")
	for _, r := range mx.Rows {
		fmt.Fprintf(w, "%-4d %-4d %-4s %-28s %-22s %-20s %-10s %-12s %-10s %-9s %s\n",
			r.Rank, r.Tier, r.Arm, truncate(r.Label, 28), truncate(r.Model, 22), successText(r.Success),
			kText(r.Reliability, r.Trials), valueText(r.Metrics[leaderboard.MetricCostPerSuccess], moneyText),
			valueText(r.Metrics[leaderboard.MetricTokens], numberText), valueText(r.Metrics[leaderboard.MetricWallTime], numberText),
			valueText(r.Metrics[leaderboard.MetricFalseCompletion], percentText))
	}
	for _, r := range mx.Rows {
		fmt.Fprintf(w, "  arm %s tier %d: %s\n", r.Arm, r.Tier, r.TierReason)
	}
	fmt.Fprintln(w, "pairs (Holm-adjusted across all pairs):")
	for _, p := range mx.Pairs {
		adj := "-"
		if p.PAdjusted != nil {
			adj = fmt.Sprintf("%.3g", *p.PAdjusted)
		}
		fmt.Fprintf(w, "  %s vs %s  tasks %d  mean diff %+.3f  p adj %s  %s\n", p.ArmX, p.ArmY, p.Tasks, p.MeanDiff, adj, p.Reason)
	}
	for _, x := range mx.Excluded {
		fmt.Fprintf(w, "  excluded %d run(s): %s\n", x.Runs, x.Reason)
	}
	fmt.Fprintln(w, mx.Note)
}

// successText writes verified success as a percentage with its interval.
func successText(v schemas.Value) string {
	x, ok := v.Float()
	if !ok {
		return "unknown"
	}
	if v.Bounds == nil {
		return percentText(x)
	}
	return fmt.Sprintf("%.1f%% [%.0f, %.0f]", 100*x, 100*v.Bounds[0], 100*v.Bounds[1])
}

// kText writes pass^k with its k, or unknown.
func kText(v schemas.Value, k int) string {
	x, ok := v.Float()
	if !ok {
		return "unknown"
	}
	return fmt.Sprintf("%s (k=%d)", percentText(x), k)
}

// valueText writes an observed value with format, else its state.
func valueText(v schemas.Value, format func(float64) string) string {
	if x, ok := v.Float(); ok {
		return format(x)
	}
	if v.State == schemas.ValueNotApplicable {
		return "n/a"
	}
	return "unknown"
}

func percentText(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func numberText(x float64) string { return fmt.Sprintf("%.0f", x) }
