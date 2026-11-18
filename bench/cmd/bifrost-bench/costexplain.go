package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

const costUsage = `usage: bifrost-bench cost explain [-config deploy/bench.yaml] -exp <id> [-a <arm>] [-b <arm>] [-condition <id>] [-grade-source original|<regrade id>] [-json]
       bifrost-bench cost pareto [-config deploy/bench.yaml] -exp <id> [-x cost_per_success|cost_per_run] [-condition <id>] [-json]
explain splits the difference in cost per run and per verified success between two arms into additive parts read from the sealed cost rows (price, input tokens, output tokens, cache read and write share, model turns, wasted attempts, failed runs) and flags a silent cache break with its likely cause (docs/cost-explain.md).
pareto places every arm on the cost against verified success plane and marks the frontier.
Both read the run folder only; no gateway is needed.`

func init() {
	register("cost", "explain why arm B cost more than arm A (cost explain), or place arms on the cost-success frontier (cost pareto)", runCost)
}

func runCost(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, costUsage)
		return exitUsage
	}
	switch args[0] {
	case "explain":
		return runCostExplain(args[1:])
	case "pareto":
		return runCostPareto(args[1:])
	default:
		fmt.Fprintln(os.Stderr, costUsage)
		return exitUsage
	}
}

func runCostExplain(args []string) int {
	fs := flag.NewFlagSet("cost explain", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "experiment id")
	a := fs.String("a", "", "baseline arm (default: the first arm)")
	b := fs.String("b", "", "arm to explain (default: the second arm)")
	condition := fs.String("condition", "", "condition (default: the experiment's primary condition)")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, or a regrade id of the experiment")
	asJSON := fs.Bool("json", false, "print the explanation as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *expID == "" {
		fmt.Fprintln(os.Stderr, costUsage)
		return exitUsage
	}
	ctx := context.Background()
	app, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	e, err := app.costExplain(ctx, *expID, *gradeSource, costexplain.Query{A: *a, B: *b, Condition: *condition})
	if errors.Is(err, costexplain.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, costUsage)
		return exitUsage
	}
	if err != nil {
		return fail("cost explain %s: %v", *expID, err)
	}
	if *asJSON {
		return printJSON(e, "cost explain "+*expID)
	}
	fmt.Print(costexplain.Markdown(e))
	return exitOK
}

func runCostPareto(args []string) int {
	fs := flag.NewFlagSet("cost pareto", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	expID := fs.String("exp", "", "experiment id")
	x := fs.String("x", costexplain.XCostPerSuccess, "cost axis: cost_per_success or cost_per_run")
	condition := fs.String("condition", "", "condition (default: the experiment's primary condition)")
	asJSON := fs.Bool("json", false, "print the points and frontier as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *expID == "" {
		fmt.Fprintln(os.Stderr, costUsage)
		return exitUsage
	}
	ctx := context.Background()
	app, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	p, err := app.matrixPareto(ctx, *expID, *condition, *x)
	if errors.Is(err, costexplain.ErrBadQuery) || errors.Is(err, leaderboard.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, costUsage)
		return exitUsage
	}
	if err != nil {
		return fail("cost pareto %s: %v", *expID, err)
	}
	if *asJSON {
		return printJSON(p, "cost pareto "+*expID)
	}
	printPareto(os.Stdout, p)
	return exitOK
}

// costExplain reads one experiment's sealed runs through the store's
// verified index and explains arm B's cost against arm A's.
func (a *app) costExplain(ctx context.Context, expID, gradeSource string, q costexplain.Query) (*costexplain.Explanation, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	runs, err := a.store.SealedRuns(ctx, expID, gradeSource)
	if err != nil {
		return nil, err
	}
	synthetic, err := leaderboard.SyntheticArms(ctx, m, runs, sealedRunFile)
	if err != nil {
		return nil, err
	}
	in := costexplain.Input{Manifest: m, Runs: runs, Read: costexplain.RunFileReader(sealedRunFile), Prices: a.prices}
	q.A, q.B = strings.TrimSpace(q.A), strings.TrimSpace(q.B)
	e, err := costexplain.Explain(ctx, in, q)
	if err != nil {
		return nil, err
	}
	e.Synthetic = synthetic[e.A.Arm] || synthetic[e.B.Arm]
	if e.Synthetic && !hasSyntheticNote(e.Notes) {
		e.Notes = append([]string{"Synthetic: scripted agent, scripted model or smoke experiment; the numbers are not findings."}, e.Notes...)
	}
	return e, nil
}

func hasSyntheticNote(notes []string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, "Synthetic:") {
			return true
		}
	}
	return false
}

// matrixPareto places every arm of one experiment from its matrix rows.
func (a *app) matrixPareto(ctx context.Context, expID, condition, x string) (*costexplain.Pareto, error) {
	mx, err := a.matrix(ctx, expID, leaderboard.MatrixQuery{Condition: condition})
	if err != nil {
		return nil, err
	}
	return costexplain.ParetoFromMatrix(mx, x)
}

// printPareto writes one line per point, frontier first.
func printPareto(w io.Writer, p *costexplain.Pareto) {
	fmt.Fprintf(w, "x: %s  y: %s\n", p.XLabel, p.Y)
	for _, pt := range p.Points {
		mark := "dominated by " + strings.Join(pt.DominatedBy, ", ")
		switch {
		case !pt.Placed:
			mark = "not placed: " + pt.Reason
		case pt.Frontier:
			mark = "frontier"
		}
		fmt.Fprintf(w, "  %-12s %-32s x %-14s y %-20s %s\n", truncate(pt.ID, 12), truncate(pt.Label, 32), costexplain.ValueText(pt.X), successText(pt.Y), mark)
	}
	fmt.Fprintln(w, p.Note)
}
