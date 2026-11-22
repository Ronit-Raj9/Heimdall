package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/bifrostfeatures"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// B2 Bifrost features under test (docs/bifrost-features.md).

const bifrostUsage = `usage: bifrost-bench bifrost cache-replays|cache-ab -exp <id> [-config deploy/bench.yaml] [-json]
cache-ab: for a "Bifrost cache on vs off" evaluation child, compares the cache-off arm with the cache-on arm on the clean tasks both ran: cache hit rate, mean latency per model request, cost saved and success change.
Traces every semantic cache hit of an experiment's sealed runs to the run that stored the cached entry (Bifrost's cache_id) and counts hits by scope: the same run, another trial or arm of the same task, or another task (a cross-task replay). Reads the run folders only; no gateway is needed.`

func init() {
	register("bifrost", "Bifrost features under test: cache-replays|cache-ab -exp <id>", runBifrostCmd)
}

func runBifrostCmd(args []string) int {
	if len(args) == 0 || (args[0] != "cache-replays" && args[0] != "cache-ab") {
		fmt.Fprintln(os.Stderr, bifrostUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("bifrost "+args[0], flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config; its runs_root is read")
	exp := fs.String("exp", "", "experiment id")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil || *exp == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, bifrostUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	if args[0] == "cache-ab" {
		runs, err := experimentCacheRuns(ctx, a.store, *exp)
		if err != nil {
			return fail("cache on vs off: %v", err)
		}
		m, err := a.store.LoadManifest(ctx, *exp)
		if err != nil {
			return fail("cache on vs off: %v", err)
		}
		off, on := cacheArms(m, runs)
		return printCacheAB(os.Stdout, *exp, bifrostfeatures.CacheAB(runs, off, on), *asJSON)
	}
	runs, err := experimentRows(ctx, a.store, *exp)
	if err != nil {
		return fail("cache replays: %v", err)
	}
	return printCacheReplays(os.Stdout, *exp, bifrostfeatures.CacheReplays(runs), *asJSON)
}

// experimentRows reads every counted sealed run's gateway rows.
func experimentRows(ctx context.Context, st *store.FSStore, expID string) ([]bifrostfeatures.RunRows, error) {
	runs, err := experimentCacheRuns(ctx, st, expID)
	if err != nil {
		return nil, err
	}
	out := make([]bifrostfeatures.RunRows, 0, len(runs))
	for _, r := range runs {
		out = append(out, r.RunRows)
	}
	return out, nil
}

// experimentCacheRuns reads every counted sealed run's gateway rows, verified
// success and cost.
func experimentCacheRuns(ctx context.Context, st *store.FSStore, expID string) ([]bifrostfeatures.CacheRun, error) {
	sealed, err := st.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		return nil, err
	}
	out := make([]bifrostfeatures.CacheRun, 0, len(sealed))
	for _, r := range sealed {
		raw, err := os.ReadFile(filepath.Join(r.Dir, "evidence.json"))
		if err != nil {
			return nil, fmt.Errorf("run %s: %w", r.Run.RunID, err)
		}
		var ev schemas.Evidence
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("run %s: evidence.json: %w", r.Run.RunID, err)
		}
		var rows []schemas.GatewayRequest
		if ev.Requests != nil {
			rows = ev.Requests.Requests
		}
		out = append(out, bifrostfeatures.CacheRunOf(r, rows))
	}
	return out, nil
}

// cacheArms are the cache-off and cache-on arms of an experiment: the
// cache-on arm by its label (B in new evaluations, "cache" in older ones),
// the cache-off arm the other arm of runs (the evaluation's own arm, "A" by
// default).
func cacheArms(m *schemas.Manifest, runs []bifrostfeatures.CacheRun) (off, on string) {
	on = schemas.VariantArmID(m, schemas.EvaluationCacheSuffix)
	if on == "" {
		on = schemas.EvaluationCacheArm
	}
	for _, r := range runs {
		if r.Arm != on {
			return r.Arm, on
		}
	}
	return "A", on
}

func printCacheAB(w io.Writer, expID string, rep bifrostfeatures.CacheABReport, asJSON bool) int {
	if asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("encode: %v", err)
		}
		fmt.Fprintf(w, "%s\n", b)
		return exitOK
	}
	fmt.Fprintf(w, "experiment %s: Bifrost cache on vs off over %d task(s)\n", expID, rep.Tasks)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ARM\tRUNS\tREQUESTS\tCACHE HITS\tHIT RATE\tMEAN LATENCY MS\tCOST USD\tSUCCESS")
	for _, a := range []bifrostfeatures.CacheArmStats{rep.Off, rep.On} {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.0f%%\t%.0f\t%.4f\t%d/%d\n", a.Arm, a.Runs, a.Requests, a.CacheHits, a.HitRate*100, a.MeanLatencyMS, a.CostUSD, a.Successes, a.SuccessRuns)
	}
	_ = tw.Flush()
	fmt.Fprintln(w, rep.Summary)
	return exitOK
}

func printCacheReplays(w io.Writer, expID string, rep bifrostfeatures.ReplayReport, asJSON bool) int {
	if asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("encode: %v", err)
		}
		fmt.Fprintf(w, "%s\n", b)
		return exitOK
	}
	fmt.Fprintf(w, "experiment %s: %d semantic cache hits; same run %d, same task (other trial or arm) %d, cross-task %d, unattributed %d\n",
		expID, rep.Hits, rep.SameRun, rep.SameTask, rep.CrossTask, rep.Unattributed)
	if len(rep.Replays) == 0 {
		return exitOK
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SCOPE\tHIT RUN\tTASK\tARM\tSTORED BY\tTASK\tARM\tCACHE ID")
	for _, r := range rep.Replays {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Scope, r.HitRun, r.HitTask, r.HitArm, orDashStr(r.StoredRun), orDashStr(r.StoredTask), orDashStr(r.StoredArm), orDashStr(r.CacheID))
	}
	_ = tw.Flush()
	if rep.CrossTask > 0 {
		fmt.Fprintf(w, "WARNING: %d replies stored for one task were served to another task\n", rep.CrossTask)
	}
	return exitOK
}

func orDashStr(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
