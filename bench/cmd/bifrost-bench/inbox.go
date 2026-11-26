package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/traceimport"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Failure inbox area (bench/traceinbox, docs/failure-inbox.md): pull flagged
// production sessions from Langfuse, LangSmith, the Bifrost gateway's logs,
// an OTLP folder or a webhook, and accept them as task drafts.

func init() {
	register("inbox", "failure inbox: pull flagged production sessions and turn them into task drafts: inbox pull|list|show|accept|reject|snooze|ingest", runInbox)
}

const inboxUsage = `usage:
  bifrost-bench inbox pull [-source <name>[,<name>]] [-since 24h|7d|<RFC 3339>] [-format text|json]
  bifrost-bench inbox list [-state new|accepted|rejected|duplicate|snoozed] [-source <name>] [-format table|json]
  bifrost-bench inbox show <id> [-format text|json]
  bifrost-bench inbox accept <id> -suite <suite> [-kind qa|tool-use|mcp|memory] [-id <task id>] [-force]
  bifrost-bench inbox reject <id> -reason "<why>" [-duplicate-of <id>]
  bifrost-bench inbox snooze <id> [-days 7]
  bifrost-bench inbox ingest -file <trace.json> [-trace-format otlp|langfuse|chat] [-reason "<why>"] [-link <url>]
  bifrost-bench inbox tasks [-suite <suite>] [-admitted] [-ids]
every subcommand also takes -config deploy/bench.yaml; sources live in inbox/sources.yaml (docs/failure-inbox.md)`

func runInbox(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ctx := context.Background()
	switch args[0] {
	case "pull":
		return inboxPull(ctx, args[1:], os.Stdout)
	case "list":
		return inboxList(ctx, args[1:], os.Stdout)
	case "show":
		return inboxShow(ctx, args[1:], os.Stdout)
	case "accept":
		return inboxAccept(ctx, args[1:], os.Stdout)
	case "reject":
		return inboxReject(args[1:], os.Stdout)
	case "snooze":
		return inboxSnooze(args[1:], os.Stdout)
	case "ingest":
		return inboxIngest(ctx, args[1:], os.Stdout)
	case "tasks":
		return inboxTasks(ctx, args[1:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown inbox subcommand %q\n%s\n", args[0], inboxUsage)
		return exitUsage
	}
}

// openInbox builds the project's inbox: inbox/sources.yaml with the bench
// config's gateway as the Bifrost source's default, the benchmarks folder,
// and the results index for the release status of accepted items.
func openInbox(ctx context.Context, config string, results bool) (*traceinbox.Inbox, *app, error) {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return nil, nil, err
	}
	ib, err := inboxFor(a, results)
	return ib, a, err
}

// inboxFor builds the inbox of an app (the CLI and serve share it).
func inboxFor(a *app, results bool) (*traceinbox.Inbox, error) {
	dir := a.cfg.Path(traceinbox.Dir)
	cfg, err := traceinbox.LoadConfig(filepath.Join(dir, traceinbox.SourcesFile))
	if err != nil {
		return nil, err
	}
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if s.Kind != traceinbox.KindBifrost {
			continue
		}
		if s.URL == "" {
			s.URL = a.cfg.Gateway.URL
		}
		if s.TokenEnv == "" {
			s.TokenEnv = a.cfg.Gateway.AdminTokenEnv
		}
	}
	ib := traceinbox.New(dir, cfg, a.cfg.Path(a.cfg.BenchmarksRoot))
	ib.ProjectRoot = a.cfg.Root
	if results {
		ib.Results = inboxResults{ix: resultsindex.New(a.store, a.logger), logger: a.logger}
	}
	return ib, nil
}

// inboxResults answers how an accepted item's task did in the latest
// experiment that ran it, from the derived results index.
type inboxResults struct {
	ix     *resultsindex.Index
	logger schemas.Logger
}

// Latest implements traceinbox.TaskResults.
func (r inboxResults) Latest(ctx context.Context, suiteID, task string) (*traceinbox.ReleaseStatus, error) {
	snap, err := r.ix.Snapshot()
	if err != nil {
		if _, serr := r.ix.Sync(ctx); serr != nil {
			return nil, serr
		}
		if snap, err = r.ix.Snapshot(); err != nil {
			return nil, err
		}
	}
	return latestRelease(snap.Runs(resultsindex.Filter{Task: task, Synthetic: "exclude"}), suiteID), nil
}

// latestRelease picks the newest experiment among a task's run rows and
// counts its verified successes.
func latestRelease(rows []resultsindex.RunRow, suiteID string) *traceinbox.ReleaseStatus {
	var latest string
	var at time.Time
	for _, r := range rows {
		if r.Suite != suiteID && !strings.HasPrefix(r.Suite, suiteID+"@") {
			continue
		}
		if latest == "" || r.ExperimentCreated.After(at) {
			latest, at = r.Experiment, r.ExperimentCreated
		}
	}
	if latest == "" {
		return &traceinbox.ReleaseStatus{Status: traceinbox.ReleaseNotRun}
	}
	rs := &traceinbox.ReleaseStatus{Experiment: latest, At: &at}
	for _, r := range rows {
		if r.Experiment != latest || r.Success == nil {
			continue
		}
		rs.Runs++
		if *r.Success >= 1 {
			rs.Passed++
		}
	}
	switch {
	case rs.Runs == 0:
		rs.Status = traceinbox.ReleaseNotRun
	case rs.Passed == rs.Runs:
		rs.Status = traceinbox.ReleasePasses
	default:
		rs.Status = traceinbox.ReleaseFails
	}
	return rs
}

func inboxFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("inbox "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.String("config", defaultConfig, "bench config")
}

func inboxPull(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("pull")
	sources := fs.String("source", "", "comma-separated source names (default: every source but the webhook)")
	since := fs.String("since", "", "start the window here instead of at each source's cursor: 24h, 7d or an RFC 3339 time")
	format := fs.String("format", "text", "text or json")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, false)
	if err != nil {
		return fail("%v", err)
	}
	opts := traceinbox.PullOptions{}
	if *sources != "" {
		opts.Sources = strings.Split(*sources, ",")
	}
	if *since != "" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			return fail("-since: %v", err)
		}
		opts.Since = t
	}
	results, err := ib.Pull(ctx, opts)
	if *format == "json" {
		_ = json.NewEncoder(w).Encode(results)
	} else {
		printPull(w, results)
	}
	if err != nil {
		return fail("inbox pull: %v", err)
	}
	return exitOK
}

// parseSince reads 24h, 7d or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := traceinbox.ParseDuration(s)
	if err != nil {
		return time.Time{}, err
	}
	return now.Add(-d), nil
}

func printPull(w io.Writer, results []traceinbox.PullResult) {
	for _, r := range results {
		if r.Error != "" {
			fmt.Fprintf(w, "%s (%s): error: %s\n", r.Source, r.Kind, r.Error)
			continue
		}
		fmt.Fprintf(w, "%s (%s): %d flagged, %d new items, %d repeats, %d already seen\n", r.Source, r.Kind, r.Fetched, r.New, r.Repeats, r.Skipped)
		if r.Truncated {
			fmt.Fprintf(w, "  the per-pull cap was reached; pull again for the rest\n")
		}
		for _, id := range r.Recurred {
			fmt.Fprintf(w, "  recurred: %s was accepted as a task and failed again in production (inbox show %s)\n", id, id)
		}
		for _, warn := range r.Warnings {
			fmt.Fprintf(w, "  warning: %s\n", warn)
		}
	}
}

func inboxList(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("list")
	state := fs.String("state", "", "only items in this state")
	source := fs.String("source", "", "only items from this source")
	format := fs.String("format", "table", "table or json")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, true)
	if err != nil {
		return fail("%v", err)
	}
	items, err := ib.List(ctx, *state, *source)
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		if items == nil {
			items = []traceinbox.Item{}
		}
		if err := json.NewEncoder(w).Encode(items); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tSEEN\tLAST SEEN\tSOURCE\tKIND\tTASK\tTITLE")
	for _, it := range items {
		task := "-"
		if it.Task != nil {
			task = traceinbox.TaskKey(it.Task) + releaseText(it.Release)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n", it.ID, it.State, it.Occurrences, it.LastSeen.Format("2006-01-02 15:04"),
			it.Source, it.SuggestedKind, task, clipRunes(it.Title, 60))
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	counts, _ := ib.Counts()
	fmt.Fprintf(w, "%d items: %d new, %d accepted, %d rejected, %d duplicate, %d snoozed; %d tasks came from production incidents\n",
		len(items), counts[traceinbox.StateNew], counts[traceinbox.StateAccepted], counts[traceinbox.StateRejected],
		counts[traceinbox.StateDuplicate], counts[traceinbox.StateSnoozed], traceinbox.RegressionCount(ib.BenchmarksRoot))
	return exitOK
}

func releaseText(r *traceinbox.ReleaseStatus) string {
	if r == nil {
		return ""
	}
	switch r.Status {
	case traceinbox.ReleasePasses:
		return fmt.Sprintf(" (passes in %s, %d/%d)", r.Experiment, r.Passed, r.Runs)
	case traceinbox.ReleaseFails:
		return fmt.Sprintf(" (FAILS in %s, %d/%d)", r.Experiment, r.Passed, r.Runs)
	}
	return " (not run yet)"
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func inboxShow(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("show")
	format := fs.String("format", "text", "text or json")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, true)
	if err != nil {
		return fail("%v", err)
	}
	it, preview, err := ib.Show(ctx, pos[0])
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"item": it, "preview": string(preview)})
		return exitOK
	}
	fmt.Fprintf(w, "%s  %s  seen %d times, %s to %s\n", it.ID, it.State, it.Occurrences, it.FirstSeen.Format(time.RFC3339), it.LastSeen.Format(time.RFC3339))
	fmt.Fprintf(w, "source %s (%s), trace %s\n", it.Source, it.SourceKind, it.TraceID)
	if it.Link != "" {
		fmt.Fprintf(w, "link %s\n", it.Link)
	}
	fmt.Fprintf(w, "signature %s (request %s, tool %s, error %s)\n", it.Signature, it.SignatureParts.Request, orDash(it.SignatureParts.Tool), orDash(it.SignatureParts.Error))
	fmt.Fprintf(w, "suggested: kind %s, suite %s\n", it.SuggestedKind, orDash(it.SuggestedSuite))
	for _, f := range it.Flagged {
		fmt.Fprintf(w, "flagged: %s\n", f)
	}
	if it.Task != nil {
		admitted := "not admitted yet"
		if it.Task.Admitted {
			admitted = "admitted"
		}
		fmt.Fprintf(w, "task %s (%s), %s%s\n", traceinbox.TaskKey(it.Task), it.Task.Dir, admitted, releaseText(it.Release))
		if it.Recurrences > 0 {
			fmt.Fprintf(w, "recurred %d times since it was accepted, last %s\n", it.Recurrences, it.LastRecurrence.Format(time.RFC3339))
		}
	}
	if it.Reason != "" {
		fmt.Fprintf(w, "reason: %s\n", it.Reason)
	}
	fmt.Fprintf(w, "\n%s", preview)
	return exitOK
}

func inboxAccept(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("accept")
	suiteID := fs.String("suite", "", "suite the draft goes into (a folder under benchmarks/)")
	kind := fs.String("kind", "", "qa, tool-use, mcp or memory (default: the suite's template)")
	id := fs.String("id", "", "task id (default: the item id)")
	force := fs.Bool("force", false, "replace an existing task folder")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *suiteID == "" {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, false)
	if err != nil {
		return fail("%v", err)
	}
	res, err := ib.Accept(ctx, pos[0], traceinbox.AcceptOptions{Suite: *suiteID, Kind: *kind, TaskID: *id, Force: *force})
	if err != nil {
		return fail("inbox accept: %v", err)
	}
	fmt.Fprintf(w, "accepted %s as task %s (tag %s, provenance.incident %s)\n", res.Item.ID, res.Task, traceinbox.RegressionTag, res.Item.ID)
	for _, step := range draftNextSteps(res) {
		fmt.Fprintln(w, step)
	}
	return exitOK
}

// draftNextSteps prints what the draft holds and what a person still has to
// do; the CLI and the API share it.
func draftNextSteps(res *traceinbox.AcceptResult) []string {
	d := res.Draft
	out := []string{fmt.Sprintf("wrote draft %s (template %s, shape of %s)", d.Dir, d.Template, d.From)}
	if d.Replayed {
		out = append(out, fmt.Sprintf("  tool service %s replays the observed calls (replay/, %s)", traceimport.ReplayServiceID, traceimport.ObservedToolsFile))
	}
	for _, m := range d.Mutants {
		out = append(out, "  "+m+": the recorded calls as a mutant; admission requires the checks to fail it")
	}
	for _, warn := range d.Warnings {
		out = append(out, "warning: "+warn)
	}
	if findings, err := suite.ScanSecrets(d.Dir); err == nil {
		for _, f := range findings {
			out = append(out, fmt.Sprintf("warning: %s; remove it before admitting", f))
		}
	}
	out = append(out, "", "The draft shows what happened in production, not what was right. next:")
	// The last two accept steps name the web page's buttons; the command
	// line says its own commands instead (wave 4, N78).
	steps := traceinbox.AcceptSteps(res, "")
	steps = append(steps[:max(len(steps)-2, 0)], "bifrost-bench task validate "+res.Task, "bifrost-bench task admit "+res.Task)
	for i, step := range steps {
		out = append(out, fmt.Sprintf("  %d. %s", i+1, step))
	}
	return out
}

func inboxReject(args []string, w io.Writer) int {
	fs, config := inboxFlags("reject")
	reason := fs.String("reason", "", "why this is not a task (required)")
	dup := fs.String("duplicate-of", "", "the item this one duplicates")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(context.Background(), *config, false)
	if err != nil {
		return fail("%v", err)
	}
	it, err := ib.Reject(pos[0], *reason, *dup)
	if err != nil {
		return fail("inbox reject: %v", err)
	}
	fmt.Fprintf(w, "%s is %s: %s\n", it.ID, it.State, it.Reason)
	return exitOK
}

func inboxSnooze(args []string, w io.Writer) int {
	fs, config := inboxFlags("snooze")
	days := fs.Int("days", 7, "days to hide the item")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(context.Background(), *config, false)
	if err != nil {
		return fail("%v", err)
	}
	it, err := ib.Snooze(pos[0], *days)
	if err != nil {
		return fail("inbox snooze: %v", err)
	}
	fmt.Fprintf(w, "%s snoozed until %s\n", it.ID, it.SnoozedUntil.Format(time.RFC3339))
	return exitOK
}

// inboxIngest pushes one local trace file through the webhook's path, for
// tools that write files and for trying the inbox out.
func inboxIngest(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("ingest")
	file := fs.String("file", "", "trace file: OTLP/JSON, a Langfuse export or a chat transcript")
	format := fs.String("trace-format", "", "otlp, langfuse or chat (default: detected)")
	reason := fs.String("reason", "", "why it was flagged")
	link := fs.String("link", "", "URL back to the trace")
	traceID := fs.String("trace-id", "", "the session to take when the file holds several")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 || *file == "" {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, false)
	if err != nil {
		return fail("%v", err)
	}
	data, err := traceimport.ReadFile(*file, traceimport.DefaultMaxBytes)
	if err != nil {
		return fail("%v", err)
	}
	src := &traceinbox.Source{Name: "manual", Kind: traceinbox.KindWebhook}
	if wh, ok := ib.Config.Webhook(); ok {
		src = wh
	}
	res, err := ib.Ingest(ctx, src, traceinbox.IngestRequest{Trace: data, Format: *format, Reason: *reason, Link: *link, TraceID: *traceID, Source: "inbox ingest"})
	if err != nil {
		return fail("inbox ingest: %v", err)
	}
	what := "one more occurrence of"
	if res.Created {
		what = "new item"
	}
	fmt.Fprintf(w, "%s %s (seen %d times); inbox show %s\n", what, res.ItemID, res.Occurrences, res.ItemID)
	if len(res.Items) > 1 {
		fmt.Fprintf(w, "the trace held %d sessions: %s\n", len(res.Items), strings.Join(res.Items, ", "))
	}
	return exitOK
}

// inboxTasks lists the tasks drafted from production incidents. -ids prints
// only their ids, one per line as a YAML list item, for a suite tier.
func inboxTasks(ctx context.Context, args []string, w io.Writer) int {
	fs, config := inboxFlags("tasks")
	suiteID := fs.String("suite", "", "only tasks of this suite")
	admitted := fs.Bool("admitted", false, "only admitted tasks")
	ids := fs.Bool("ids", false, "print task ids as a YAML list (for a regression tier)")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, inboxUsage)
		return exitUsage
	}
	ib, _, err := openInbox(ctx, *config, false)
	if err != nil {
		return fail("%v", err)
	}
	tasks, err := traceinbox.IncidentTasks(ib.BenchmarksRoot)
	if err != nil {
		return fail("%v", err)
	}
	keys := make([]string, 0, len(tasks))
	for k := range tasks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if !*ids {
		fmt.Fprintln(tw, "TASK\tINCIDENT\tFIRST SEEN\tSOURCE\tADMITTED")
	}
	n := 0
	for _, k := range keys {
		t := tasks[k]
		if *suiteID != "" && t.Suite != *suiteID {
			continue
		}
		ok := traceinbox.TaskAdmitted(ib.BenchmarksRoot, t.Suite, t.Task)
		if *admitted && !ok {
			continue
		}
		n++
		if *ids {
			fmt.Fprintf(w, "- %s\n", t.Task)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\n", k, t.Incident.ID, t.Incident.FirstSeen, orDash(t.Incident.Source), ok)
	}
	if !*ids {
		if err := tw.Flush(); err != nil {
			return fail("%v", err)
		}
		fmt.Fprintf(w, "%d tasks came from production incidents\n", n)
	}
	return exitOK
}

// incidentSection is the report section that names production incidents
// whose tasks ran in the experiment (Failure inbox area, docs/failure-inbox.md).
// A benchmarks folder that cannot be read leaves the report as it is.
func (a *app) incidentSection(runs []schemas.SealedRun, report string) string {
	incidents, err := traceinbox.IncidentTasks(a.cfg.Path(a.cfg.BenchmarksRoot))
	if err != nil {
		a.logger.Warn("incident tasks could not be read for the report", "error", err.Error())
		return report
	}
	return insertSection(report, traceinbox.IncidentSection(runs, incidents))
}

// serveInbox builds the inbox for serve. A sources file that does not load
// is logged and the inbox routes answer 503, so a typo in it never stops the
// server.
func serveInbox(a *app) handlers.InboxSource {
	ib, err := inboxFor(a, true)
	if err != nil {
		a.logger.Warn("failure inbox is off", "error", err.Error())
		return nil
	}
	svc := traceinbox.NewService(ib)
	if wh, ok := ib.Config.Webhook(); ok && svc.Gate() == nil {
		a.logger.Warn("the inbox webhook is off: its token variable is empty", "token_env", wh.TokenEnv)
	}
	return svc
}

// addInboxSchedule adds the inbox's scheduled pull to the daemon's schedules
// when inbox/sources.yaml sets schedule: and schedules.yaml does not already
// name a schedule inbox-pull. A sources file that does not load is logged.
func addInboxSchedule(a *app, file *schedule.File) {
	cfg, err := traceinbox.LoadConfig(filepath.Join(a.cfg.Path(traceinbox.Dir), traceinbox.SourcesFile))
	if err != nil {
		a.logger.Warn("inbox sources not loaded; no scheduled inbox pull", "error", err.Error())
		return
	}
	if cfg.Schedule == "" {
		return
	}
	if _, ok := file.Find(schedule.JobInboxPull); ok {
		return
	}
	file.Schedules = append(file.Schedules, schedule.Schedule{Name: schedule.JobInboxPull, Job: schedule.JobInboxPull, Cron: cfg.Schedule})
}

// runScheduleJob runs a built-in schedule job. inbox-pull pulls every
// source once; it fails when every source failed. A recurrence of an
// accepted incident is named in the summary, so the run record shows it.
func runScheduleJob(ctx context.Context, a *app, s schedule.Schedule) (*schedule.Result, error) {
	if s.Job != schedule.JobInboxPull {
		return nil, fmt.Errorf("schedule %s: unknown job %q", s.Name, s.Job)
	}
	ib, err := inboxFor(a, false)
	if err != nil {
		return nil, err
	}
	results, err := ib.Pull(ctx, traceinbox.PullOptions{})
	var parts []string
	for _, r := range results {
		line := fmt.Sprintf("%s: %d new, %d repeats", r.Source, r.New, r.Repeats)
		if r.Error != "" {
			line = r.Source + ": error " + r.Error
		}
		if len(r.Recurred) > 0 {
			line += "; recurred after accept: " + strings.Join(r.Recurred, ", ")
		}
		parts = append(parts, line)
	}
	return &schedule.Result{Summary: strings.Join(parts, "; ")}, err
}
