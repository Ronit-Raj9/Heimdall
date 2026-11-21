package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskhealth"
)

// W7 suite tiers (docs/suite-tiers.md): `suite tiers suggest` proposes the
// pr, nightly and headroom tiers of a suite from task health and observed
// wall time, as a suite.yaml snippet to paste.

func init() {
	suiteSubcommands["tiers"] = runSuiteTiers
}

const suiteTiersUsage = `usage: heimdall suite tiers suggest [-config deploy/bench.yaml | -root benchmarks -runs runs] [-max-minutes 10] [-max-tasks 10] [-trials 1] [-workers 4] [-headroom 0.5] [-json] <suite id>`

func runSuiteTiers(args []string) int {
	if len(args) < 1 || args[0] != "suggest" {
		fmt.Fprintln(os.Stderr, suiteTiersUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("suite tiers suggest", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	runs := fs.String("runs", "", "run store folder (default: runs_root of the config)")
	config := fs.String("config", defaultConfig, "bench config")
	maxMinutes := fs.Float64("max-minutes", 10, "wall-clock budget of one pr run")
	maxTasks := fs.Int("max-tasks", 10, "most tasks in the pr tier")
	trials := fs.Int("trials", 1, "trials per arm the pr preset runs")
	workers := fs.Int("workers", 4, "concurrent runs")
	headroom := fs.Float64("headroom", 0.5, "headroom tier: observed pass rate below this")
	asJSON := fs.Bool("json", false, "print the suggestion as JSON")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteTiersUsage)
		return exitUsage
	}
	benchRoot, cfg, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	runsRoot := *runs
	if runsRoot == "" {
		if cfg == nil {
			return fail("suite tiers suggest: no run store; pass -runs <folder> or a readable -config")
		}
		runsRoot = cfg.Path(cfg.RunsRoot)
	}
	ctx := context.Background()
	s, err := loadSuite(ctx, benchRoot, pos[0])
	if err != nil {
		return fail("load suite %s: %v", pos[0], err)
	}
	src, err := openHealthSource(runsRoot)
	if err != nil {
		return fail("%v", err)
	}
	hf, err := suite.ReadHealthFile(s.Root)
	if err != nil {
		return fail("%v", err)
	}
	h, err := taskhealth.Compute(ctx, src, s, hf.Quarantined, taskhealth.Options{})
	if err != nil {
		return fail("%v", err)
	}
	walls, err := taskWallTimes(ctx, src, s.ID)
	if err != nil {
		return fail("%v", err)
	}
	sug := suite.SuggestTiers(s.ID, tierCandidates(h, walls), suite.SuggestOptions{
		MaxMinutes: *maxMinutes, MaxTasks: *maxTasks, Trials: *trials, Workers: *workers, Headroom: *headroom})
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sug); err != nil {
			return fail("encode: %v", err)
		}
		return exitOK
	}
	printTiers(os.Stdout, sug)
	return exitOK
}

// tierCandidates joins task health with median wall times.
func tierCandidates(h *schemas.SuiteHealth, walls map[string]float64) []suite.TierCandidate {
	out := make([]suite.TierCandidate, 0, len(h.Tasks))
	for _, t := range h.Tasks {
		c := suite.TierCandidate{Task: t.Task, PassRate: t.Difficulty.PassRate, Discrimination: t.Difficulty.Discrimination,
			Spread: t.Difficulty.Spread, Flaky: t.Verifier.Flaky, Quarantined: t.Quarantine != nil}
		if w, ok := walls[t.Task]; ok {
			c.WallS = &w
		}
		out = append(out, c)
	}
	return out
}

// taskWallTimes is the median observed wall_time_s per task over every
// counted sealed run of the suite in the run store, scripted agents and smoke
// experiments left out (as task health leaves them out): a scripted run's
// seconds say nothing about a real agent's.
func taskWallTimes(ctx context.Context, src taskhealth.Source, suiteID string) (map[string]float64, error) {
	exps, err := src.Experiments(ctx)
	if err != nil {
		return nil, err
	}
	per := map[string][]float64{}
	for _, id := range exps {
		m, err := src.Manifest(ctx, id)
		if err != nil || m.SuiteID != suiteID {
			continue
		}
		runs, err := src.SealedRuns(ctx, id, "")
		if err != nil {
			continue
		}
		for _, r := range runs {
			if !r.Slot.Counted || m.Experiment.NoPaidTraffic() || m.Arms[r.Slot.Arm].Config.Adapter == "scripted" {
				continue
			}
			if x, ok := r.Metrics["wall_time_s"].Float(); ok && x > 0 {
				per[r.Slot.Task] = append(per[r.Slot.Task], x)
			}
		}
	}
	out := map[string]float64{}
	for t, xs := range per {
		out[t] = stats.Median(xs)
	}
	return out, nil
}

func printTiers(w io.Writer, s suite.TierSuggestion) {
	fmt.Fprintf(w, "# suggested tiers for %s (pr: at most %.0f minutes with %d trial(s) x %d arms over %d workers)\n", s.Suite, s.MaxMinutes, s.Trials, s.Arms, s.Workers)
	fmt.Fprintln(w, "tiers:")
	names := make([]string, 0, len(s.Tiers))
	for n := range s.Tiers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sel := s.Tiers[n]
		if sel.All {
			fmt.Fprintf(w, "  %s: all\n", n)
			continue
		}
		fmt.Fprintf(w, "  %s: [%s]\n", n, strings.Join(sel.IDs, ", "))
	}
	if s.PRMinutes != nil {
		fmt.Fprintf(w, "# pr estimated wall clock: %.1f minutes\n", *s.PRMinutes)
	}
	tasks := make([]string, 0, len(s.Reasons))
	for t := range s.Reasons {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	for _, t := range tasks {
		fmt.Fprintf(w, "#   %s: %s\n", t, s.Reasons[t])
	}
	for _, n := range s.Notes {
		fmt.Fprintf(w, "# note: %s\n", n)
	}
}
