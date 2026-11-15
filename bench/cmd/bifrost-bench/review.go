package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

func init() {
	register("review", "human review of sealed runs: review list|show|add|log (overrule a grade or flag a run; sealed evidence is never edited)", runReview)
}

const reviewUsage = `usage: bifrost-bench review <list|show|add|log> ...
  review list -exp <exp_id> [-reason split_votes|unparsable_votes|invalid_evaluator|veto|false_completion|flagged] [-json]
  review show <run_id> [-exp <exp_id>] [-json]
  review add -run <run_id> -decision pass|fail|invalid|flag_only -reason "why" -reviewer <name> [-evidence item ...] [-exp <exp_id>]
  review log -exp <exp_id> [-json]
reviews apply only to: bifrost-bench check -exp <exp_id> -grade-source reviewed`

// exitReviewUnverified is review log's exit code when the chain does not
// verify.
const exitReviewUnverified = 1

// maxReviewRows caps the reviewed-run rows a report lists.
const maxReviewRows = 40

func runReview(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, reviewUsage)
		return exitUsage
	}
	switch args[0] {
	case "list":
		return runReviewList(args[1:])
	case "show":
		return runReviewShow(args[1:])
	case "add":
		return runReviewAdd(args[1:])
	case "log":
		return runReviewLog(args[1:])
	}
	fmt.Fprintln(os.Stderr, reviewUsage)
	return exitUsage
}

// evidenceList collects repeated -evidence flags.
type evidenceList []string

func (e *evidenceList) String() string { return strings.Join(*e, ",") }

func (e *evidenceList) Set(v string) error {
	*e = append(*e, v)
	return nil
}

func runReviewList(args []string) int {
	fs := flag.NewFlagSet("review list", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id")
	reason := fs.String("reason", "", "keep only runs with this queue reason")
	asJSON := fs.Bool("json", false, "print the queue as JSON")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, reviewUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	q, err := a.store.ReviewQueue(ctx, *exp, *reason)
	if err != nil {
		return fail("review list: %v", err)
	}
	if *asJSON {
		return printReviewJSON(os.Stdout, q)
	}
	writeReviewQueue(os.Stdout, q)
	return exitOK
}

func runReviewShow(args []string) int {
	fs := flag.NewFlagSet("review show", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id (default: found from the run id)")
	asJSON := fs.Bool("json", false, "print the run's reviews as JSON")
	runID, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || runID == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, reviewUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	expID, err := reviewExp(a.store, *exp, runID)
	if err != nil {
		return fail("review show: %v", err)
	}
	rr, err := a.store.RunReviews(ctx, expID, runID)
	if err != nil {
		return fail("review show: %v", err)
	}
	if *asJSON {
		return printReviewJSON(os.Stdout, rr)
	}
	writeRunReviews(os.Stdout, rr)
	return exitOK
}

func runReviewAdd(args []string) int {
	fs := flag.NewFlagSet("review add", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id (default: found from the run id)")
	runID := fs.String("run", "", "run id to review")
	decision := fs.String("decision", "", "pass, fail, invalid or flag_only")
	reason := fs.String("reason", "", "why, in plain text (required)")
	reviewer := fs.String("reviewer", "", "your name (required; self-declared, there is no login)")
	var evidence evidenceList
	fs.Var(&evidence, "evidence", "evidence you relied on (a file, gate, judge vote or note); repeat for more")
	if err := fs.Parse(args); err != nil || *runID == "" || *decision == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, reviewUsage)
		return exitUsage
	}
	req := &schemas.ReviewRequest{RunID: *runID, Decision: schemas.ReviewDecision(*decision), Reason: *reason, Reviewer: *reviewer, Evidence: evidence}
	if err := store.ValidateReviewRequest(req); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	if mode := a.cfg.Server.Mode; mode != schemas.ModeLive {
		return fail("review add: refused in %s mode: the run folder is a fixture, not live results", mode)
	}
	expID, err := reviewExp(a.store, *exp, *runID)
	if err != nil {
		return fail("review add: %v", err)
	}
	rv, err := a.store.AddReview(ctx, expID, req)
	if err != nil {
		return fail("review add: %v", err)
	}
	fmt.Printf("review %s of run %s: %s (outcome before: %s, sealed: %s)\nhash %s\n", rv.ReviewID, rv.RunID, rv.Decision,
		rv.PreviousOutcome, rv.SealedOutcome, rv.Hash)
	if rv.Supersedes != "" {
		fmt.Printf("supersedes review %s (kept in the log)\n", rv.Supersedes)
	}
	fmt.Printf("analyze with: bifrost-bench check -exp %s -grade-source reviewed\n", expID)
	return exitOK
}

func runReviewLog(args []string) int {
	fs := flag.NewFlagSet("review log", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id")
	asJSON := fs.Bool("json", false, "print the log as JSON")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, reviewUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	l, err := a.store.ReviewLog(ctx, *exp)
	if err != nil {
		return fail("review log: %v", err)
	}
	if *asJSON {
		return printReviewJSON(os.Stdout, l)
	}
	writeReviewLog(os.Stdout, l)
	if !l.Verified {
		return exitReviewUnverified
	}
	return exitOK
}

// reviewSummary sets the analysis's human review counts from the runs it
// was decided on and adds a Human review section to the report: under grade
// source reviewed, which runs a review decided and which reviews were
// ignored; under any other source, how many reviewed runs it did not apply.
// An experiment without reviews gets no section.
func (a *app) reviewSummary(ctx context.Context, an *schemas.Analysis, runs []schemas.SealedRun, report string) string {
	an.ReviewedRuns, an.ReviewChangedRuns = store.ReviewCounts(runs)
	set, err := a.store.Reviews(ctx, an.ExpID)
	if err != nil {
		a.logger.Warn("reviews could not be read for the report", "exp_id", an.ExpID, "error", err.Error())
		return report
	}
	return insertSection(report, reviewMarkdown(an, runs, set))
}

// reviewMarkdown renders the Human review section, or "" when the
// experiment has no review and no review problem.
func reviewMarkdown(an *schemas.Analysis, runs []schemas.SealedRun, set *store.ReviewSet) string {
	problems := append([]string(nil), set.Problems...)
	pending := 0
	for _, r := range runs {
		_, p := store.EffectiveReview(set.ForRun(r.Run.RunID), r.SealHash)
		problems = append(problems, p...)
		if cur, _ := store.EffectiveReview(set.ForRun(r.Run.RunID), r.SealHash); cur != nil && r.Review == nil && len(r.Problems) == 0 {
			pending++
		}
	}
	if len(set.Valid) == 0 && len(problems) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Human review\n\n")
	if an.GradeSource == schemas.GradeSourceReviewed {
		fmt.Fprintf(&b, "%d counted runs took their outcome from a human review; %d of them differ from the sealed outcome. "+
			"A reviewed outcome is a person's judgement (evidence level `reviewed`), never a verifier result. The sealed runs are "+
			"unchanged, and the analysis on grade source original stays as it was.\n\n", an.ReviewedRuns, an.ReviewChangedRuns)
		writeReviewedRuns(&b, runs)
	} else {
		fmt.Fprintf(&b, "%d counted runs have a human review that this analysis does not apply (grade source %s). "+
			"Analyze with `bifrost-bench check -exp %s -grade-source reviewed` to apply them.\n\n", pending, an.GradeSource, an.ExpID)
	}
	if len(problems) > 0 {
		b.WriteString("Reviews ignored because they do not verify:\n\n")
		for _, p := range problems {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// writeReviewedRuns lists the runs a review decided, at most maxReviewRows.
func writeReviewedRuns(b *strings.Builder, runs []schemas.SealedRun) {
	n := 0
	for _, r := range runs {
		if r.Review == nil {
			continue
		}
		if n == 0 {
			b.WriteString("| Run | Task | Arm | Condition | Sealed | Reviewed | Reviewer | Review | Reason |\n|---|---|---|---|---|---|---|---|---|\n")
		}
		n++
		if n > maxReviewRows {
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", r.Run.RunID, r.Slot.Task, r.Slot.Arm, r.Slot.Condition,
			r.Review.SealedOutcome, r.Run.Outcome, reviewCell(r.Review.Reviewer), r.Review.ReviewID, reviewCell(r.Review.Reason))
	}
	if n > maxReviewRows {
		fmt.Fprintf(b, "\n%d more reviewed runs not listed; `bifrost-bench review log` has them all.\n", n-maxReviewRows)
	}
	if n > 0 {
		b.WriteString("\n")
	}
}

// reviewCell makes free text safe for one Markdown table cell.
func reviewCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:117] + "..."
	}
	return strings.ReplaceAll(s, "|", "/")
}

// reviewSource adapts the store to handlers.ReviewSource, finding a run's
// experiment from its id.
type reviewSource struct {
	st *store.FSStore
}

func (r reviewSource) ReviewQueue(ctx context.Context, expID, reason string) (*schemas.ReviewQueue, error) {
	return r.st.ReviewQueue(ctx, expID, reason)
}

func (r reviewSource) ReviewLog(ctx context.Context, expID string) (*schemas.ReviewLog, error) {
	return r.st.ReviewLog(ctx, expID)
}

func (r reviewSource) RunReviews(ctx context.Context, runID string) (*schemas.RunReviews, error) {
	expID, err := r.st.RunExp(runID)
	if err != nil {
		return nil, err
	}
	return r.st.RunReviews(ctx, expID, runID)
}

func (r reviewSource) AddReview(ctx context.Context, req *schemas.ReviewRequest) (*schemas.Review, error) {
	expID, err := reviewExp(r.st, req.ExpID, req.RunID)
	if err != nil {
		return nil, err
	}
	return r.st.AddReview(ctx, expID, req)
}

// reviewExp is the experiment named on the command line, or the one the run
// belongs to.
func reviewExp(st *store.FSStore, exp, runID string) (string, error) {
	if exp != "" {
		return exp, nil
	}
	return st.RunExp(runID)
}

// splitRunArg splits a leading positional argument from the flags after it.
func splitRunArg(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
}

func printReviewJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

func writeReviewQueue(w io.Writer, q *schemas.ReviewQueue) {
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tTASK\tARM\tCONDITION\tTRIAL\tSEALED\tCURRENT\tDECISION\tREASONS")
	for _, it := range q.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", it.RunID, it.Task, it.Arm, it.Condition, it.Trial,
			it.SealedOutcome, it.Outcome, reviewDash(string(it.Decision)), strings.Join(it.Reasons, ","))
	}
	_ = tw.Flush()
	fmt.Fprintf(w, "\n%d runs need review\n", len(q.Items))
	writeReviewProblems(w, q.Problems)
}

func writeRunReviews(w io.Writer, rr *schemas.RunReviews) {
	fmt.Fprintf(w, "run %s (experiment %s)\nsealed outcome: %s\noutcome under grade source reviewed: %s\n", rr.RunID, rr.ExpID, rr.SealedOutcome, rr.Outcome)
	if len(rr.Reasons) > 0 {
		fmt.Fprintf(w, "needs review: %s\n", strings.Join(rr.Reasons, ", "))
	}
	if len(rr.Reviews) == 0 {
		fmt.Fprintln(w, "no reviews")
	}
	for _, r := range rr.Reviews {
		mark := " "
		if rr.Current != nil && rr.Current.ReviewID == r.ReviewID {
			mark = "*"
		}
		fmt.Fprintf(w, "%s %s  %s  %-9s by %s: %s\n", mark, r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), r.ReviewID, r.Decision, r.Reviewer, r.Reason)
		if len(r.Evidence) > 0 {
			fmt.Fprintf(w, "    evidence: %s\n", strings.Join(r.Evidence, "; "))
		}
	}
	writeReviewProblems(w, rr.Problems)
}

func writeReviewLog(w io.Writer, l *schemas.ReviewLog) {
	for _, r := range l.Reviews {
		fmt.Fprintf(w, "%s  %s  run %s  %-9s by %s: %s\n", r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), r.ReviewID, r.RunID, r.Decision, r.Reviewer, r.Reason)
	}
	state := "verified"
	if !l.Verified {
		state = "DOES NOT VERIFY"
	}
	fmt.Fprintf(w, "\n%d reviews; chain %s\n", len(l.Reviews), state)
	writeReviewProblems(w, l.Problems)
}

func writeReviewProblems(w io.Writer, problems []string) {
	if len(problems) == 0 {
		return
	}
	fmt.Fprintln(w, "\nproblems:")
	for _, p := range problems {
		fmt.Fprintf(w, "  - %s\n", p)
	}
}

func reviewDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
