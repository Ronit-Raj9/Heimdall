package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

// Cross-experiment paired test (docs/control-evals.md): the same arm in two
// experiments that differ only in realism or framing, paired by task.

const pairedUsage = `usage: bifrost-bench paired -exp <a> -exp <b> [-arm A] [-condition clean] [-metric verified_success] [-grade-source original] [-json] [-config deploy/bench.yaml]
Pairs the counted sealed runs of one arm in two experiments by task under one
condition and prints the paired difference (b minus a) with the exact
sign-flip p-value. The experiments must share the suite, its lock and the
arm's agent config; realism and framing may differ. Use it for awareness
deltas measured in experiments run apart (realism standard against neutral).`

func init() {
	register("paired", "paired test of one arm across two experiments that differ only in realism or framing", runPaired)
}

func runPaired(args []string) int {
	fs := flag.NewFlagSet("paired", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	var exps expList
	fs.Var(&exps, "exp", "experiment id; give exactly two")
	arm := fs.String("arm", verdict.ArmBaseline, "arm to pair")
	cond := fs.String("condition", schemas.ConditionClean, "condition to pair under")
	metric := fs.String("metric", verdict.MetricVerifiedSuccess, "metric")
	gradeSource := fs.String("grade-source", "original", "original or a regrade id")
	asJSON := fs.Bool("json", false, "print the effect as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, pairedUsage)
		return exitUsage
	}
	ids := splitList(exps.String())
	if len(ids) != 2 {
		fmt.Fprintln(os.Stderr, pairedUsage)
		return exitUsage
	}
	cfg, err := loadConfig(*config)
	if err != nil {
		return fail("%v", err)
	}
	st, err := store.NewFSStore(cfg.Path(cfg.RunsRoot), utils.NewStdLogger(os.Stderr, false))
	if err != nil {
		return fail("open run store: %v", err)
	}
	return pairedRun(context.Background(), os.Stdout, st, ids[0], ids[1], *arm, *cond, *metric, *gradeSource, *asJSON)
}

// pairedSource is what the command reads; *store.FSStore satisfies it.
type pairedSource interface {
	LoadManifest(ctx context.Context, expID string) (*schemas.Manifest, error)
	SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error)
}

func pairedRun(ctx context.Context, w io.Writer, src pairedSource, a, b, arm, cond, metric, gradeSource string, asJSON bool) int {
	ma, err := src.LoadManifest(ctx, a)
	if err != nil {
		return fail("experiment %s: %v", a, err)
	}
	mb, err := src.LoadManifest(ctx, b)
	if err != nil {
		return fail("experiment %s: %v", b, err)
	}
	if err := verdict.CrossMatch(ma, mb, arm); err != nil {
		return fail("experiments %s and %s cannot be paired: %v", a, b, err)
	}
	ra, err := src.SealedRuns(ctx, a, gradeSource)
	if err != nil {
		return fail("experiment %s: %v", a, err)
	}
	rb, err := src.SealedRuns(ctx, b, gradeSource)
	if err != nil {
		return fail("experiment %s: %v", b, err)
	}
	e := verdict.PairedAcross(a, b, ra, rb, arm, cond, metric)
	if asJSON {
		out, err := json.MarshalIndent(e, "", "  ")
		if err != nil {
			return fail("encode effect: %v", err)
		}
		fmt.Fprintln(w, string(out))
		return exitOK
	}
	fmt.Fprintf(w, "arm %s, %s on %s: %s (realism %s) against %s (realism %s)\n", arm, metric, cond,
		b, realismOf(mb), a, realismOf(ma))
	fmt.Fprintf(w, "paired tasks %d (%d up, %d down), mean difference %+.4f", e.NTasks, e.NPositive, e.NNegative, e.DBar)
	if e.PValue != nil {
		fmt.Fprintf(w, ", sign-flip p %.4f", *e.PValue)
	}
	if e.Interval != nil {
		fmt.Fprintf(w, ", paired t 95%% [%+.4f, %+.4f]", e.Interval[0], e.Interval[1])
	}
	fmt.Fprintln(w)
	if e.NTasks == 0 {
		fmt.Fprintln(w, "no task has counted runs of this arm in both experiments")
	}
	fmt.Fprintln(w, "A null difference does not show the agent behaves the same when it can tell it is tested.")
	return exitOK
}

func realismOf(m *schemas.Manifest) string {
	if m.Experiment.Realism == "" {
		return schemas.RealismStandard
	}
	return m.Experiment.Realism
}
