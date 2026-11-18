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

const bifrostUsage = `usage: bifrost-bench bifrost cache-replays -exp <id> [-config deploy/bench.yaml] [-json]
Traces every semantic cache hit of an experiment's sealed runs to the run that stored the cached entry (Bifrost's cache_id) and counts hits by scope: the same run, another trial or arm of the same task, or another task (a cross-task replay). Reads the run folders only; no gateway is needed.`

func init() {
	register("bifrost", "Bifrost features under test: cache-replays -exp <id>", runBifrostCmd)
}

func runBifrostCmd(args []string) int {
	if len(args) == 0 || args[0] != "cache-replays" {
		fmt.Fprintln(os.Stderr, bifrostUsage)
		return exitUsage
	}
	fs := flag.NewFlagSet("bifrost cache-replays", flag.ContinueOnError)
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
	runs, err := experimentRows(ctx, a.store, *exp)
	if err != nil {
		return fail("cache replays: %v", err)
	}
	return printCacheReplays(os.Stdout, *exp, bifrostfeatures.CacheReplays(runs), *asJSON)
}

// experimentRows reads every counted sealed run's gateway rows.
func experimentRows(ctx context.Context, st *store.FSStore, expID string) ([]bifrostfeatures.RunRows, error) {
	sealed, err := st.SealedRuns(ctx, expID, store.GradeSourceOriginal)
	if err != nil {
		return nil, err
	}
	out := make([]bifrostfeatures.RunRows, 0, len(sealed))
	for _, r := range sealed {
		rr := bifrostfeatures.RunRows{RunID: r.Run.RunID, Task: r.Slot.Task, Arm: r.Slot.Arm, Condition: r.Slot.Condition, Trial: r.Slot.Trial}
		raw, err := os.ReadFile(filepath.Join(r.Dir, "evidence.json"))
		if err != nil {
			return nil, fmt.Errorf("run %s: %w", r.Run.RunID, err)
		}
		var ev schemas.Evidence
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("run %s: evidence.json: %w", r.Run.RunID, err)
		}
		if ev.Requests != nil {
			rr.Rows = ev.Requests.Requests
		}
		out = append(out, rr)
	}
	return out, nil
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
