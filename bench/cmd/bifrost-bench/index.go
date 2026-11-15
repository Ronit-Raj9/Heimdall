package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/trends"
)

func init() {
	register("index", "derived results index over sealed runs, for trends and search (never read by a verdict): index rebuild|status|query", runIndex)
}

const indexUsage = `usage: bifrost-bench index rebuild [-config deploy/bench.yaml]
       bifrost-bench index status  [-config deploy/bench.yaml]
       bifrost-bench index query   [-config deploy/bench.yaml] [-format table|json] [-limit n] [-analyses] [-no-sync] [key=value ...]
query keys: experiment run_id suite task arm agent model condition outcome config_hash synthetic(include|exclude|only) since until
The index is derived from sealed run folders and can be deleted and rebuilt at any time.
Verdicts, the leaderboard and releases never read it.`

func runIndex(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, indexUsage)
		return exitUsage
	}
	sub := args[0]
	fs := flag.NewFlagSet("index "+sub, flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	format := fs.String("format", "table", "query output: table or json")
	limit := fs.Int("limit", 0, "query: at most this many rows (0: all)")
	analyses := fs.Bool("analyses", false, "query: list analysis rows instead of run rows")
	noSync := fs.Bool("no-sync", false, "query: read the index as it is, without the incremental update first")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, indexUsage)
		return exitUsage
	}
	if *format != "table" && *format != "json" {
		fmt.Fprintf(os.Stderr, "format %q is not table or json\n%s\n", *format, indexUsage)
		return exitUsage
	}
	if sub != "query" && fs.NArg() != 0 || (sub != "rebuild" && sub != "status" && sub != "query") {
		fmt.Fprintln(os.Stderr, indexUsage)
		return exitUsage
	}
	f, err := resultsindex.ParseFilter(fs.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, indexUsage)
		return exitUsage
	}
	f.Limit = *limit
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	ix := resultsindex.New(a.store, a.logger)
	switch sub {
	case "rebuild":
		st, err := ix.Rebuild(ctx)
		if err != nil {
			return fail("%v", err)
		}
		return printIndexStatus(os.Stdout, ix, st)
	case "status":
		snap, err := ix.Snapshot()
		if err != nil {
			return fail("%v", err)
		}
		return printIndexStatus(os.Stdout, ix, snap.Status())
	}
	if !*noSync {
		if _, err := ix.Sync(ctx); err != nil {
			return fail("%v", err)
		}
	}
	snap, err := ix.Snapshot()
	if err != nil {
		return fail("%v", err)
	}
	if *analyses {
		return printAnalysisRows(os.Stdout, snap.Analyses(f.Experiment), *format)
	}
	return printRunRows(os.Stdout, snap.Runs(f), *format)
}

// refreshResultsIndex updates the results index for one experiment after its
// runs sealed. The index is derived data, so a failure is only logged: the
// sealed folders are intact and `index rebuild` recovers.
func refreshResultsIndex(ctx context.Context, a *app, expID string) {
	if err := resultsindex.New(a.store, a.logger).UpdateExperiment(ctx, expID); err != nil {
		a.logger.Warn("results index not updated; run `bifrost-bench index rebuild`", "exp_id", expID, "error", err.Error())
	}
}

// sealRefresher updates the results index in the background after a run
// seals. The scheduler drops events a busy reader does not take, so the
// progress loop only hands the experiment id over and never waits for the
// index; a burst of seals collapses into one update.
type sealRefresher struct {
	once sync.Once
	kick chan string
}

var indexRefresher sealRefresher

// indexOnSeal starts an index update for the experiment when ev is a run
// seal. It never blocks. An update cut short by the process ending leaves the
// previous index file in place; the next `index query`, trends request or
// `index rebuild` catches up.
func indexOnSeal(ctx context.Context, a *app, expID string, ev engine.Event) {
	if ev.Kind != engine.EventRunSealed {
		return
	}
	indexRefresher.once.Do(func() {
		indexRefresher.kick = make(chan string, 1)
		go func() {
			bg := context.WithoutCancel(ctx)
			for id := range indexRefresher.kick {
				refreshResultsIndex(bg, a, id)
			}
		}()
	})
	select {
	case indexRefresher.kick <- expID:
	default: // an update is already queued; it reads the new seal too
	}
}

// trendSource serves GET /trends from the results index, bringing it up to
// date incrementally first. The verdict, leaderboard and release paths never
// use it.
type trendSource struct {
	ix     *resultsindex.Index
	logger schemas.Logger
}

// Trends implements handlers.TrendSource. A failed sync is logged and the
// index is read as it is: a trend may lag, but it never blocks the page.
func (s trendSource) Trends(ctx context.Context, q trends.Query) (*trends.Response, error) {
	if _, err := s.ix.Sync(ctx); err != nil {
		s.logger.Warn("results index sync failed; serving trends from the index as it is", "error", err.Error())
	}
	snap, err := s.ix.Snapshot()
	if err != nil {
		return nil, err
	}
	return trends.Build(snap, q, time.Now())
}

// printIndexStatus prints the index status and every rejected entry.
func printIndexStatus(w io.Writer, ix *resultsindex.Index, st resultsindex.Status) int {
	fmt.Fprintf(w, "results index (%s) %s\n", st.Format, st.Path)
	if !st.Exists {
		fmt.Fprintln(w, "  not built yet: run `bifrost-bench index rebuild`")
		return exitOK
	}
	fmt.Fprintf(w, "  built %s, updated %s\n", indexStamp(st.BuiltAt), indexStamp(st.UpdatedAt))
	fmt.Fprintf(w, "  %d experiments, %d runs, %d analyses, %d rejected\n", st.Experiments, st.Runs, st.Analyses, st.Rejected)
	snap, err := ix.Snapshot()
	if err != nil {
		return fail("%v", err)
	}
	for _, r := range snap.Rejected() {
		fmt.Fprintf(w, "  rejected %s %s: %s\n", r.Experiment, r.RunID, r.Reason)
	}
	return exitOK
}

// printRunRows prints run rows as a table or as JSON.
func printRunRows(w io.Writer, rows []resultsindex.RunRow, format string) int {
	if format == "json" {
		return writeIndexJSON(w, map[string]any{"runs": indexNonNil(rows)})
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "EXPERIMENT\tRUN\tSUITE\tTASK\tARM\tAGENT\tMODEL\tCONDITION\tTRIAL\tOUTCOME\tSUCCESS\tCOST_USD\tWALL_S\tSTARTED\tSYNTHETIC")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%t\n", r.Experiment, r.RunID, r.Suite, r.Task,
			r.Arm, r.Agent, r.Model, r.Condition, r.Trial, r.Outcome, indexNum(r.Success, 0), indexNum(r.CostUSD, 4), indexNum(r.WallTimeS, 1),
			indexStamp(r.Started), r.Synthetic)
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(w, "%d runs\n", len(rows))
	return exitOK
}

// printAnalysisRows prints analysis rows as a table or as JSON.
func printAnalysisRows(w io.Writer, rows []resultsindex.AnalysisRow, format string) int {
	if format == "json" {
		return writeIndexJSON(w, map[string]any{"analyses": indexNonNil(rows)})
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "EXPERIMENT\tANALYSIS\tDECISION\tACTION\tPOLICY\tGRADE_SOURCE\tCREATED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Experiment, r.AnalysisID, r.Decision, r.Action,
			indexDash(r.Policy), r.GradeSource, indexStamp(r.CreatedAt))
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(w, "%d analyses\n", len(rows))
	return exitOK
}

// writeIndexJSON writes v as indented JSON.
func writeIndexJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("encode: %v", err)
	}
	return exitOK
}

// indexNonNil returns an empty slice for nil, so JSON says [] rather than null.
func indexNonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// indexNum formats an optional number, "-" when it was not observed.
func indexNum(v *float64, prec int) string {
	if v == nil {
		return "-"
	}
	return strconv.FormatFloat(*v, 'f', prec, 64)
}

// indexStamp formats a time in UTC to the minute, "-" for the zero time.
func indexStamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04Z")
}

// indexDash returns s, or "-" when it is empty.
func indexDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
