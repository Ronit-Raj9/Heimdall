package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
)

// W3 customer support: simulator validity (docs/customer-support.md).

const simulatorUsage = `usage: bifrost-bench simulator spread [-config deploy/bench.yaml] -a <exp id> -b <exp id> [-condition clean] [-json]
spread compares two experiments that ran the same agents on the same support suite with two different simulator models (an experiment's simulator: block) and reports, per arm, how much verified success moved when only the simulated user changed. It warns when that spread is at least the effect between arms that experiment A would claim: such an effect may come from the simulated user, not the agents.
It reads sealed runs only; no gateway is needed. Exit 0 without a warning, 1 with one.`

func init() {
	register("simulator", "simulated user validity: success spread across two simulator models (simulator spread)", runSimulator)
}

func runSimulator(args []string) int {
	if len(args) == 0 || args[0] != "spread" {
		fmt.Fprintln(os.Stderr, simulatorUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("simulator spread", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	config := fs.String("config", defaultConfig, "bench config")
	expA := fs.String("a", "", "experiment with the reference simulator model")
	expB := fs.String("b", "", "the same experiment with another simulator model")
	condition := fs.String("condition", schemas.ConditionClean, "condition whose runs are compared")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *expA == "" || *expB == "" {
		fmt.Fprintln(os.Stderr, simulatorUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	var sides [2]simuser.SpreadSide
	for i, id := range []string{*expA, *expB} {
		if sides[i], err = a.spreadSide(ctx, id, *condition); err != nil {
			return fail("simulator spread: %s: %v", id, err)
		}
	}
	rep, err := simuser.Spread(sides[0], sides[1])
	if err != nil {
		return fail("simulator spread: %v", err)
	}
	if *asJSON {
		if code := printJSON(rep, "simulator spread"); code != exitOK {
			return code
		}
	} else {
		printSpread(os.Stdout, rep)
	}
	if rep.Warn {
		return 1
	}
	return exitOK
}

// spreadSide reads one experiment's effective simulator and per-arm success.
func (a *app) spreadSide(ctx context.Context, expID, condition string) (simuser.SpreadSide, error) {
	m, err := a.store.LoadManifest(ctx, expID)
	if err != nil {
		return simuser.SpreadSide{}, err
	}
	sim := m.Simulator // frozen at plan time; older manifests fall back to the suite on disk
	if sim == nil {
		suite, err := engine.LoadSuite(ctx, a.reg, m.Experiment.Suite)
		if err != nil {
			return simuser.SpreadSide{}, fmt.Errorf("load suite %s: %w", m.Experiment.Suite, err)
		}
		sim = simuser.Effective(suite, &m.Experiment)
	}
	runs, err := a.store.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return simuser.SpreadSide{}, err
	}
	return simuser.SpreadSide{
		ExpID: expID, Suite: m.SuiteID + "@" + m.SuiteVersion,
		Simulator: sim,
		Success:   simuser.SuccessFromRuns(runs, condition),
	}, nil
}

func printSpread(w io.Writer, r *simuser.SpreadReport) {
	fmt.Fprintf(w, "simulator spread on %s\n  A %s: %s\n  B %s: %s\n\n", r.Suite, r.ExpA, r.SimulatorA, r.ExpB, r.SimulatorB)
	fmt.Fprintf(w, "  %-16s %10s %10s %8s\n", "arm", "success A", "success B", "spread")
	for _, a := range r.Arms {
		fmt.Fprintf(w, "  %-16s %9.0f%% %9.0f%% %7.0fpt\n", a.Arm, 100*a.SuccessA, 100*a.SuccessB, 100*a.Spread)
	}
	fmt.Fprintf(w, "\n%s\n", r.Message)
}
