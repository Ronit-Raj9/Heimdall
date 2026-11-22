package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// X6 live scoring (bench/livescore, docs/live-scoring.md): score real
// sessions read from the Bifrost gateway, compare two live versions with an
// always-valid sequential test, alert, and send the worst failures to the
// failure inbox. Read-only towards the gateway.

func init() {
	register("livescore", "live traffic scoring from the Bifrost gateway (read-only): livescore init|pull|status|compare", runLivescore)
}

const livescoreUsage = `usage:
  bifrost-bench livescore init [-url <gateway>] [-token-env NAME] [-baseline-key <vk id> -candidate-key <vk id>]
                               [-tag-header x-bf-lh-agent-version -baseline-tag v1 -candidate-tag v2] [-force]
  bifrost-bench livescore pull [-format text|json]
  bifrost-bench livescore status [-format text|json]
  bifrost-bench livescore compare [-format text|json] [-fail-on worse] [-restart]
  bifrost-bench livescore compare -simulate <streams> [-sessions 600] [-effect 0] [-format text|json]
every subcommand also takes -config deploy/bench.yaml; settings live in livescore/config.yaml (docs/live-scoring.md)`

func runLivescore(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, livescoreUsage)
		return exitUsage
	}
	ctx := context.Background()
	switch args[0] {
	case "init":
		return livescoreInit(ctx, args[1:], os.Stdout)
	case "pull":
		return livescorePull(ctx, args[1:], os.Stdout)
	case "status":
		return livescoreStatus(ctx, args[1:], os.Stdout)
	case "compare":
		return livescoreCompare(ctx, args[1:], os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown livescore subcommand %q\n%s\n", args[0], livescoreUsage)
		return exitUsage
	}
}

func livescoreFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("livescore "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs, fs.String("config", defaultConfig, "bench config")
}

// scorerFor builds the project's scorer: livescore/config.yaml with the
// bench config's gateway as the default source, the failure inbox's store,
// the webhook sender (its log beside the scores) and bench.yaml's alerts.
func scorerFor(a *app) (*livescore.Scorer, error) { return scorerWith(a, true) }

// scorerWith builds the scorer; withJudge false leaves the judge out without
// reading its key variable (the hosted pull adds the org's own key).
func scorerWith(a *app, withJudge bool) (*livescore.Scorer, error) {
	dir := a.cfg.Path(livescore.Dir)
	cfg, err := livescore.LoadConfig(filepath.Join(dir, livescore.ConfigFile))
	if err != nil {
		return nil, err
	}
	s := &livescore.Scorer{Config: cfg, Store: livescore.NewStore(dir), Inbox: traceinbox.NewFileStore(a.cfg.Path(traceinbox.Dir)),
		URL: a.cfg.Gateway.URL, TokenEnv: a.cfg.Gateway.AdminTokenEnv,
		Notifier: alerts.NewSender(filepath.Join(dir, "deliveries.jsonl")), ExtraAlerts: a.cfg.Alerts}
	if j := cfg.Judge; j != nil && withJudge {
		prompt := ""
		if j.PromptFile != "" {
			b, err := os.ReadFile(a.cfg.Path(j.PromptFile))
			if err != nil {
				return nil, fmt.Errorf("judge prompt: %w", err)
			}
			prompt = string(b)
		}
		key := os.Getenv(j.KeyEnv)
		if key == "" {
			a.logger.Warn("live judge is off: its key variable is empty", "key_env", j.KeyEnv)
		} else {
			calib := ""
			if j.Calibration != "" {
				calib = a.cfg.Path(j.Calibration)
			}
			s.Judge = livescore.NewChatJudge(j, key, prompt, calib, nil)
		}
	}
	return s, nil
}

func openScorer(ctx context.Context, config string) (*livescore.Scorer, *app, error) {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return nil, nil, err
	}
	s, err := scorerFor(a)
	return s, a, err
}

func livescoreInit(ctx context.Context, args []string, w io.Writer) int {
	fs, config := livescoreFlags("init")
	var o livescore.InitOptions
	fs.StringVar(&o.URL, "url", "", "gateway URL (default: gateway.url of the bench config)")
	fs.StringVar(&o.TokenEnv, "token-env", "", "variable holding the read credential (default: gateway.admin_token_env)")
	fs.StringVar(&o.BaselineKey, "baseline-key", "", "virtual key id of the version in production")
	fs.StringVar(&o.CandidateKey, "candidate-key", "", "virtual key id of the new version")
	fs.StringVar(&o.TagHeader, "tag-header", "", "a header both versions send, e.g. x-bf-lh-agent-version")
	fs.StringVar(&o.BaselineTag, "baseline-tag", "", "the baseline's value of -tag-header")
	fs.StringVar(&o.CandidateTag, "candidate-tag", "", "the candidate's value of -tag-header")
	force := fs.Bool("force", false, "overwrite an existing livescore/config.yaml")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, livescoreUsage)
		return exitUsage
	}
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	text, err := livescore.Template(o)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitUsage
	}
	if _, err := livescore.ParseConfig([]byte(text), "template"); err != nil && o.BaselineKey+o.TagHeader != "" {
		return fail("the config from these flags does not validate: %v", err)
	}
	dir := a.cfg.Path(livescore.Dir)
	path := filepath.Join(dir, livescore.ConfigFile)
	if _, err := os.Stat(path); err == nil && !*force {
		return fail("%s exists; pass -force to overwrite it", path)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("%v", err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return fail("%v", err)
	}
	fmt.Fprintf(w, "wrote %s\n", path)
	if o.BaselineKey == "" && o.TagHeader == "" {
		fmt.Fprintln(w, "next: replace the EDIT ME virtual keys (or use tag:), then run `bifrost-bench livescore pull`")
	} else {
		fmt.Fprintln(w, "next: export the read credential variable and run `bifrost-bench livescore pull`")
	}
	return exitOK
}

func livescorePull(ctx context.Context, args []string, w io.Writer) int {
	fs, config := livescoreFlags("pull")
	format := fs.String("format", "text", "text or json")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, livescoreUsage)
		return exitUsage
	}
	s, _, err := openScorer(ctx, *config)
	if err != nil {
		return fail("%v", err)
	}
	res, err := s.Pull(ctx)
	if *format == "json" && res != nil {
		_ = json.NewEncoder(w).Encode(res)
	} else if res != nil {
		printLivePull(w, res)
	}
	if err != nil {
		return fail("livescore pull: %v", err)
	}
	return exitOK
}

func printLivePull(w io.Writer, r *livescore.PullResult) {
	fmt.Fprintf(w, "live pull %s, window %s to %s (evidence: live)\n", r.At.Format(time.RFC3339), r.Window[0].Format(time.RFC3339), r.Window[1].Format(time.RFC3339))
	for v, n := range r.Rows {
		fmt.Fprintf(w, "  %s: %d rows read, %d sessions scored\n", v, n, r.Sessions[v])
	}
	for v, e := range r.Errors {
		fmt.Fprintf(w, "  %s: error %s\n", v, e)
	}
	fmt.Fprintf(w, "  %d still open, %d outside the sample, %d detail rows\n", r.Open, r.Skipped, r.Details)
	if len(r.Inbox) > 0 {
		fmt.Fprintf(w, "  inbox items: %s\n", strings.Join(r.Inbox, ", "))
	}
	for _, d := range r.Decided {
		fmt.Fprintf(w, "  decided: %s\n", d)
	}
	for _, a := range r.Alerts {
		fmt.Fprintf(w, "  alert sent: %s\n", a)
	}
	if r.Truncated {
		fmt.Fprintln(w, "  a per-pull cap was hit; pull again for the rest")
	}
	for _, wn := range r.Warnings {
		fmt.Fprintf(w, "  warning: %s\n", wn)
	}
}

func livescoreStatus(ctx context.Context, args []string, w io.Writer) int {
	fs, config := livescoreFlags("status")
	format := fs.String("format", "text", "text or json")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, livescoreUsage)
		return exitUsage
	}
	s, _, err := openScorer(ctx, *config)
	if err != nil {
		return fail("%v", err)
	}
	st, err := s.Status(time.Now().UTC())
	if err != nil {
		return fail("%v", err)
	}
	if *format == "json" {
		_ = json.NewEncoder(w).Encode(st)
		return exitOK
	}
	fmt.Fprintf(w, "live scoring of %s (read-only, evidence: live)\n%s\n\n", st.Source.URL, st.Note)
	if st.Source.LastPull != nil {
		fmt.Fprintf(w, "last pull %s, %d sessions scored", st.Source.LastPull.Format(time.RFC3339), st.Source.LastScored)
		if st.Source.LastError != "" {
			fmt.Fprintf(w, ", error: %s", st.Source.LastError)
		}
		fmt.Fprintln(w)
	} else {
		fmt.Fprintln(w, "no pull yet")
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tSESSIONS\tFLAGGED\tSELECTOR")
	for _, v := range st.Versions {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", v.Name, v.Sessions, v.Flagged, v.Selector)
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\nsessions: %d by session id, %d by idle gap (%d concurrent); %s\n", st.Grouping.BySessionID, st.Grouping.ByGap, st.Grouping.Concurrent, st.Grouping.Reliability)
	if st.Compare != nil {
		fmt.Fprintln(w)
		printLiveCompare(w, st.Compare)
	}
	if len(st.Worst) > 0 {
		fmt.Fprintln(w, "\nworst sessions:")
		for _, r := range st.Worst[:min(len(st.Worst), 10)] {
			fmt.Fprintf(w, "  %s %s %s severity %d [%s] inbox %s\n", r.ID, r.Version, r.Ended.Format(time.RFC3339), r.Severity, strings.Join(r.Flags, ","), orDash(r.InboxItem))
		}
	}
	return exitOK
}

func printLiveCompare(w io.Writer, c *livescore.Comparison) {
	fmt.Fprintf(w, "comparison %s (candidate) vs %s (baseline): %s, %d and %d sessions, alpha %.3g over %d signals (each one-sided test at %.2g, e-value threshold %.0f), min %d pairs\n",
		c.Candidate, c.Baseline, c.Result, c.SessionsB, c.SessionsA, c.Alpha, len(c.Signals), c.PerTestLevel, c.Threshold, c.MinPairs)
	if c.Stale != "" {
		fmt.Fprintf(w, "  stale: %s\n", c.Stale)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  SIGNAL\tPAIRS\tBASELINE\tCANDIDATE\tE WORSE\tE BETTER\tDECISION")
	for _, s := range c.Signals {
		need := ""
		if s.NeedPairs > 0 {
			need = fmt.Sprintf(" (needs %d more pairs)", s.NeedPairs)
		}
		fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%.2f\t%.2f\t%s%s\n", s.Name, s.Pairs, meanText(s.MeanA, s), meanText(s.MeanB, s), s.EWorse, s.EBetter, s.Decision, need)
	}
	_ = tw.Flush()
}

func meanText(m *float64, s livescore.SignalResult) string {
	if m == nil {
		return "-"
	}
	if s.Kind == livescore.KindBounded {
		return fmt.Sprintf("%.1f%%", *m*100)
	}
	return fmt.Sprintf("%.4g", *m)
}

func livescoreCompare(ctx context.Context, args []string, w io.Writer) int {
	fs, config := livescoreFlags("compare")
	format := fs.String("format", "text", "text or json")
	failOn := fs.String("fail-on", "", "worse: exit 1 when the comparison decided worse")
	restart := fs.Bool("restart", false, "start a new comparison from now on (old sessions are not tested again)")
	simulate := fs.Int("simulate", 0, "run the comparison on this many synthetic A/A streams instead (no gateway)")
	sessions := fs.Int("sessions", 600, "-simulate: sessions per version per stream")
	effect := fs.Float64("effect", 0, "-simulate: the candidate's added abandonment rate (0 is A/A)")
	if pos, err := parseArgs(fs, args); err != nil || len(pos) != 0 || (*failOn != "" && *failOn != "worse") {
		fmt.Fprintln(os.Stderr, livescoreUsage)
		return exitUsage
	}
	if *simulate > 0 {
		if *simulate > 100000 || *sessions < 1 || *sessions > 100000 || *effect < 0 || *effect > 1 {
			fmt.Fprintln(os.Stderr, "error: -simulate 1 to 100000, -sessions 1 to 100000, -effect 0 to 1")
			return exitUsage
		}
		r := livescore.Simulate(livescore.SimConfig{Streams: *simulate, Sessions: *sessions, Effect: *effect, Seed: uint64(time.Now().UnixNano())})
		if *format == "json" {
			_ = json.NewEncoder(w).Encode(r)
		} else {
			fmt.Fprintf(w, "%d synthetic streams, %d sessions per version, effect %.3f, %d signals, alpha %.2f\n", r.Streams, r.Sessions, r.Effect, r.Signals, r.Alpha)
			fmt.Fprintf(w, "streams with any decision: %d (rate %.4f, standard error %.4f); decided worse: %d\n", r.AnyDecision, r.Rate, r.StdErr, r.WorseDecisions)
			fmt.Fprintf(w, "by signal: %v\n%s\n", r.BySignal, r.Description)
		}
		return exitOK
	}
	s, _, err := openScorer(ctx, *config)
	if err != nil {
		return fail("%v", err)
	}
	var c *livescore.Comparison
	if *restart {
		c, err = s.RestartCompare()
	} else {
		c, err = s.Compare()
	}
	if err != nil {
		return fail("%v", err)
	}
	if c == nil {
		if s.Config.Compare == nil {
			return fail("no compare section in livescore/config.yaml")
		}
		fmt.Fprintln(w, "no comparison yet: run `bifrost-bench livescore pull` first")
		return exitOK
	}
	if *format == "json" {
		_ = json.NewEncoder(w).Encode(c)
	} else {
		printLiveCompare(w, c)
		fmt.Fprintf(w, "\n%s\n", c.Method)
	}
	if *failOn == "worse" && c.Result == livescore.ResultWorse {
		return 1
	}
	return exitOK
}

// liveSource serves the Live page and API. The config is read on every
// request, so an edited config applies without a restart; a config that
// does not load answers as not configured with the reason.
// With offline set (serve -offline), Pull is refused: it would read the
// gateway and could call a paid judge.
type liveSource struct {
	a       *app
	offline bool
}

func serveLivescore(a *app, offline bool) handlers.LivescoreSource {
	return liveSource{a: a, offline: offline}
}

func (l liveSource) scorer() (*livescore.Scorer, error) { return scorerFor(l.a) }

// Status implements handlers.LivescoreSource.
func (l liveSource) Status(_ context.Context) (*livescore.Status, error) {
	s, err := l.scorer()
	if errors.Is(err, livescore.ErrNotConfigured) {
		// No settings file is not a problem, only "not set up yet" (N131):
		// Problem means a file exists and did not load.
		return &livescore.Status{Evidence: livescore.EvidenceClass, Note: livescore.StatusNote, Versions: []livescore.VersionView{},
			Signals: livescore.SignalDefs(), Worst: []livescore.SessionRecord{}, Events: []livescore.LogEvent{}}, nil
	}
	if err != nil {
		return &livescore.Status{Evidence: livescore.EvidenceClass, Note: livescore.StatusNote, Versions: []livescore.VersionView{},
			Signals: livescore.SignalDefs(), Worst: []livescore.SessionRecord{}, Events: []livescore.LogEvent{},
			Problem: "the live scoring settings file could not be read: " + err.Error()}, nil
	}
	return s.Status(time.Now().UTC())
}

// Sessions implements handlers.LivescoreSource.
func (l liveSource) Sessions(_ context.Context, f livescore.SessionFilter) (*livescore.SessionList, error) {
	s, err := l.scorer()
	if err != nil {
		return nil, err
	}
	return s.Sessions(f)
}

// Compare implements handlers.LivescoreSource.
func (l liveSource) Compare(_ context.Context) (*livescore.Comparison, error) {
	s, err := l.scorer()
	if err != nil {
		return nil, err
	}
	return s.Compare()
}

// Pull implements handlers.LivescoreSource.
func (l liveSource) Pull(ctx context.Context) (*livescore.PullResult, error) {
	if l.offline {
		return nil, livescore.ErrOffline
	}
	s, err := l.scorer()
	if err != nil {
		return nil, err
	}
	return s.Pull(ctx)
}

// unattendedScorer turns off the paid judge of a scorer the daemon runs and
// reports whether one was on: its calls would be outside every spend cap and
// the daemon's ledger (QC2-15-06).
func unattendedScorer(s *livescore.Scorer) bool {
	if s == nil || s.Judge == nil {
		return false
	}
	s.Judge = nil
	return true
}

// addLivescoreSchedule adds the scheduled pull when livescore/config.yaml
// sets schedule: and schedules.yaml names no schedule livescore-pull.
func addLivescoreSchedule(a *app, file *schedule.File) {
	cfg, err := livescore.LoadConfig(filepath.Join(a.cfg.Path(livescore.Dir), livescore.ConfigFile))
	if err != nil {
		if !errors.Is(err, livescore.ErrNotConfigured) {
			a.logger.Warn("live scoring config not loaded; no scheduled live pull", "error", err.Error())
		}
		return
	}
	if cfg.Schedule == "" {
		return
	}
	if _, ok := file.Find(schedule.JobLivescorePull); ok {
		return
	}
	file.Schedules = append(file.Schedules, schedule.Schedule{Name: schedule.JobLivescorePull, Job: schedule.JobLivescorePull, Cron: cfg.Schedule})
}

// runLivescoreSchedule is the livescore-pull job: one pull. It spends
// nothing: it reads only, and a scheduled pull never calls the paid judge
// (unattendedScorer), since the daemon has no daily cap or cost record for
// a built-in job.
func runLivescoreSchedule(ctx context.Context, a *app, _ schedule.Schedule) (*schedule.Result, error) {
	s, err := scorerFor(a)
	if err != nil {
		return nil, err
	}
	if unattendedScorer(s) {
		a.logger.Warn("scheduled live pull runs without the judge: a built-in schedule has no daily cap or cost record for paid calls; run `livescore pull` by hand to judge")
	}
	res, err := s.Pull(ctx)
	if res == nil {
		return nil, err
	}
	total := 0
	for _, n := range res.Sessions {
		total += n
	}
	sum := fmt.Sprintf("live pull: %d sessions scored, %d inbox items", total, len(res.Inbox))
	if len(res.Decided) > 0 {
		sum += "; decided " + strings.Join(res.Decided, ", ")
	}
	return &schedule.Result{Summary: sum}, err
}
