package main

// W8 judge calibration (docs/judge-calibration.md): `judge calibrate`,
// `judge label`, `judge status`, the calibration routes' source and the
// judge_calibration instrument wired into every analysis.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("judge", "judge calibration: judge calibrate|label|status (agreement of a judged suite's judge with human labels)", runJudge)
}

const judgeUsage = `usage: bifrost-bench judge <calibrate|label|status> ...
  judge calibrate <suite> [-yes] [-from-reviews -exp <exp_id> ...] [-seed-controls] [-policy <file>]
      run the pinned judge on every labelled item (forecast first; asks before spending)
  judge label <suite> -task <id> (-answer <text> | -answer-file <file> | -run <run_id>) -label correct|incorrect -labeller <name> [-note <text>]
  judge label <suite> -import <file.csv|file.jsonl>
  judge label <suite> -seed-controls       add the suite's expected and wrong answers as weak seed items
  judge status [<suite>] [-policy <file>] [-json]`

func runJudge(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, judgeUsage)
		return exitUsage
	}
	switch args[0] {
	case "calibrate":
		return runJudgeCalibrate(args[1:])
	case "label":
		return runJudgeLabel(args[1:])
	case "status":
		return runJudgeStatus(args[1:])
	}
	fmt.Fprintln(os.Stderr, judgeUsage)
	return exitUsage
}

// stringList collects a repeated flag.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// judgeSvc is the calibration side of the app: suites by id, labels,
// status. It implements handlers.CalibrationSource.
type judgeSvc struct {
	a *app
}

// suiteRoot is the folder of suite id under the benchmarks root, or
// ErrNotFound.
func (j judgeSvc) suiteRoot(id string) (string, error) {
	if id == "" || strings.ContainsAny(id, "/\\@") || id == "." || id == ".." {
		return "", fmt.Errorf("suite %q: %w", id, schemas.ErrNotFound)
	}
	root := filepath.Join(j.a.cfg.Path(j.a.cfg.BenchmarksRoot), id)
	if fi, err := os.Stat(filepath.Join(root, suite.SuiteFile)); err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("suite %s: %w", id, schemas.ErrNotFound)
	}
	return root, nil
}

func (j judgeSvc) load(ctx context.Context, id string) (*schemas.Suite, error) {
	if _, err := j.suiteRoot(id); err != nil {
		return nil, err
	}
	return loadSuite(ctx, j.a.cfg.Path(j.a.cfg.BenchmarksRoot), id)
}

// status is the calibration status of one loaded suite.
func (j judgeSvc) status(s *schemas.Suite, th schemas.JudgeCalibrationThresholds) (*schemas.CalibrationStatus, error) {
	var tmpls map[string]string
	if s.Judge != nil && s.Judge.PromptVersion != "" {
		tmpls, _ = graders.PromptTemplates(s.Judge.PromptVersion)
	}
	return calibration.Status(s, tmpls, th, time.Now().UTC())
}

// CalibrationStatus implements handlers.CalibrationSource with the default
// thresholds.
func (j judgeSvc) CalibrationStatus(ctx context.Context, id string) (*schemas.CalibrationStatus, error) {
	s, err := j.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return j.status(s, calibration.Thresholds(nil, ""))
}

// CalibrationLabels implements handlers.CalibrationSource.
func (j judgeSvc) CalibrationLabels(_ context.Context, id string) (*schemas.CalibrationLabels, error) {
	root, err := j.suiteRoot(id)
	if err != nil {
		return nil, err
	}
	set, err := calibration.LoadSet(root)
	if err != nil {
		return nil, err
	}
	items := set.Items
	if items == nil {
		items = []schemas.CalibrationItem{}
	}
	return &schemas.CalibrationLabels{Suite: id, SetSHA256: set.SHA256, Items: items}, nil
}

// AddCalibrationLabel implements handlers.CalibrationSource: one label
// from the labelling page, for a task of the suite.
func (j judgeSvc) AddCalibrationLabel(ctx context.Context, id string, req *schemas.CalibrationLabelRequest) (*schemas.CalibrationItem, error) {
	s, err := j.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.TaskByID(strings.TrimSpace(req.Task)) == nil {
		return nil, fmt.Errorf("task %s is not in suite %s: %w", req.Task, id, calibration.ErrInput)
	}
	it, err := calibration.NewItem(*req, schemas.CalibrationSourceLabel, time.Now())
	if err != nil {
		return nil, err
	}
	if _, err := calibration.Append(s.Root, []schemas.CalibrationItem{it}); err != nil {
		return nil, err
	}
	return &it, nil
}

// thresholdsFrom reads the judge_calibration section of a release policy
// file; an empty path gives the defaults.
func thresholdsFrom(path string) (schemas.JudgeCalibrationThresholds, error) {
	if path == "" {
		return calibration.Thresholds(nil, ""), nil
	}
	p, _, err := policy.Load(path)
	if err != nil {
		return schemas.JudgeCalibrationThresholds{}, err
	}
	return calibration.Thresholds(p.JudgeCalibration, path), nil
}

// reviewLabels turns the human reviews of an experiment's sealed runs into
// calibration items of suite s: each run whose newest decision review
// (written against its seal) is pass or fail, with the answer the run
// sealed. invalid and flag_only reviews say nothing about correctness and
// are skipped, as are runs of other suites and runs without an answer.
func reviewLabels(ctx context.Context, st *store.FSStore, expID string, s *schemas.Suite) ([]schemas.CalibrationItem, []string, error) {
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return nil, nil, err
	}
	if m.SuiteID != s.ID {
		return nil, nil, fmt.Errorf("experiment %s ran suite %s, not %s", expID, m.SuiteID, s.ID)
	}
	runs, err := st.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return nil, nil, err
	}
	set, err := st.Reviews(ctx, expID)
	if err != nil {
		return nil, nil, err
	}
	skipped := append([]string(nil), set.Problems...)
	var out []schemas.CalibrationItem
	for _, sr := range runs {
		if len(sr.Problems) > 0 {
			continue
		}
		rv, _ := store.EffectiveReview(set.ForRun(sr.Run.RunID), sr.SealHash)
		if rv == nil {
			continue
		}
		label := map[schemas.ReviewDecision]string{schemas.ReviewPass: schemas.CalibrationCorrect, schemas.ReviewFail: schemas.CalibrationIncorrect}[rv.Decision]
		if label == "" {
			continue
		}
		if s.TaskByID(sr.Slot.Task) == nil {
			skipped = append(skipped, fmt.Sprintf("run %s: task %s is not in suite %s", sr.Run.RunID, sr.Slot.Task, s.ID))
			continue
		}
		answer, problem, err := graders.ReadAnswer(sr.Dir)
		if err != nil || problem != "" {
			skipped = append(skipped, fmt.Sprintf("run %s: %s%v", sr.Run.RunID, problem, errOrNil(err)))
			continue
		}
		it, err := calibration.NewItem(schemas.CalibrationLabelRequest{
			Task: sr.Slot.Task, Answer: answer, Label: label, Labeller: rv.Reviewer, Note: rv.Reason, Ref: rv.ReviewID,
		}, schemas.CalibrationSourceReview, rv.CreatedAt)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("run %s: %v", sr.Run.RunID, err))
			continue
		}
		out = append(out, it)
	}
	return out, skipped, nil
}

func errOrNil(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// seedLabels are the suite's admission controls as weak seed items.
func seedLabels(s *schemas.Suite) []schemas.CalibrationItem {
	var out []schemas.CalibrationItem
	for _, t := range s.Tasks {
		ans, err := graders.LoadAnswerSpec(t)
		if err != nil {
			continue
		}
		out = append(out, calibration.SeedItems(t.ID, ans.Expected, ans.WrongOnes, time.Now())...)
	}
	return out
}

// calibrateOptions are `judge calibrate`'s inputs.
type calibrateOptions struct {
	yes      bool
	thresh   schemas.JudgeCalibrationThresholds
	judgeCfg func() (graders.JudgeConfig, error) // the wired judge, built only after consent
}

// calibrateSuite forecasts, asks, runs the judge on the suite's labels and
// writes the record. Nothing is spent without -yes or a yes at the prompt;
// without a terminal and without -yes it prints the forecast and stops.
func calibrateSuite(ctx context.Context, a *app, s *schemas.Suite, opts calibrateOptions, in io.Reader, out io.Writer) (*schemas.CalibrationRecord, error) {
	set, err := calibration.LoadSet(s.Root)
	if err != nil {
		return nil, err
	}
	human, seed, disputed := calibration.Resolve(set.Items)
	if len(human)+len(seed) == 0 {
		return nil, fmt.Errorf("suite %s has no calibration labels: add some with `judge label`, `judge calibrate -from-reviews -exp <id>` or `judge label -seed-controls`", s.ID)
	}
	var price func(string, schemas.Tokens) (float64, bool)
	if a != nil && a.prices != nil {
		price = graders.PricesFrom(a.prices.Lookup)
	}
	plan, err := graders.PlanCalibration(s, append(append([]calibration.Labelled{}, human...), seed...), price)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "judge %s/%s, prompt %s, %d vote(s) per call\n", plan.Spec.Provider, plan.Spec.Model, plan.Spec.PromptVersion, plan.Spec.Votes)
	fmt.Fprintf(out, "items: %d human-labelled, %d admission-control seeds (weak, reported apart), %d disputed (left out), %d skipped\n",
		len(human), len(seed), disputed, len(plan.Skipped))
	for _, sk := range plan.Skipped {
		fmt.Fprintf(out, "  skipped: %s\n", sk)
	}
	if len(human) < opts.thresh.MinLabels {
		fmt.Fprintf(out, "warning: %d human labels, fewer than %d: the intervals will be wide\n", len(human), opts.thresh.MinLabels)
	}
	cost := "unpriced (" + plan.PriceNote + ")"
	if plan.Priced {
		cost = fmt.Sprintf("about $%.4f", plan.USD)
	}
	fmt.Fprintf(out, "forecast: %d judge calls (first pass, repeat pass, %s), about %d input and %d output tokens, %s\n",
		plan.Calls, strings.Join(calibration.Perturbations, " and "), plan.InputTokens, plan.OutTokens, cost)
	if len(plan.Items) == 0 {
		return nil, errors.New("no item can be judged")
	}
	if !opts.yes {
		if !judgeInteractive(in) {
			fmt.Fprintf(out, "not run: this spends real model money; rerun with -yes\n")
			return nil, nil
		}
		fmt.Fprintf(out, "run %d judge calls? [y/N] ", plan.Calls)
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if v := strings.ToLower(strings.TrimSpace(answer)); v != "y" && v != "yes" {
			fmt.Fprintln(out, "not run")
			return nil, nil
		}
	}
	cfg, err := opts.judgeCfg()
	if err != nil {
		return nil, err
	}
	runs, spend, err := graders.RunCalibration(ctx, cfg, plan, func(done, total int) {
		if done == total || done%10 == 0 {
			fmt.Fprintf(out, "  judged %d of %d items\n", done, total)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("calibrate: %w (no record written)", err)
	}
	snap, err := calibration.Snapshot(set)
	if err != nil {
		return nil, err
	}
	// The record covers only the templates its items were judged with; the
	// prompt hash is over the whole version, for `judge status`.
	used := map[string]string{}
	for _, r := range runs {
		used[r.Template] = plan.Templates[r.Template]
	}
	rec := &schemas.CalibrationRecord{
		Suite: s.ID, SuiteVersion: s.Version, Provider: plan.Spec.Provider, Model: plan.Spec.Model, PromptVersion: plan.Spec.PromptVersion,
		PromptSHA256: calibration.PromptHash(plan.Templates), Templates: used, Votes: plan.Spec.Votes,
		ServedModels: spend.ServedModels, SetSHA256: set.SHA256, SetRows: len(set.Items), Calls: spend.Calls, USD: spend.USD,
		PriceRef: cfg.PriceRef, CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	calibration.Build(rec, runs, disputed)
	if err := calibration.WriteRecord(s.Root, rec); err != nil {
		return nil, fmt.Errorf("write calibration record: %w", err)
	}
	fmt.Fprintf(out, "record %s written (set %s, snapshot %s)\n", rec.RecordID, rec.SetSHA256[:12], snap)
	writeRecordSummary(out, rec, opts.thresh)
	return rec, nil
}

// judgeInteractive reports whether in is a terminal.
func judgeInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func runJudgeCalibrate(args []string) int {
	fs := flag.NewFlagSet("judge calibrate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	yes := fs.Bool("yes", false, "run without asking, even though the judge calls cost money")
	fromReviews := fs.Bool("from-reviews", false, "first add the pass and fail reviews of the -exp experiments as labels")
	seed := fs.Bool("seed-controls", false, "first add the suite's expected and wrong answers as weak seed items")
	policyFile := fs.String("policy", "", "release policy file whose judge_calibration thresholds apply (default: the documented defaults)")
	var exps stringList
	fs.Var(&exps, "exp", "experiment whose reviews become labels (with -from-reviews); repeat for more")
	id, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || id == "" || fs.NArg() != 0 || (*fromReviews && len(exps) == 0) {
		fmt.Fprintln(os.Stderr, judgeUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	j := judgeSvc{a: a}
	s, err := j.load(ctx, id)
	if err != nil {
		return fail("judge calibrate: %v", err)
	}
	th, err := thresholdsFrom(*policyFile)
	if err != nil {
		return fail("judge calibrate: %v", err)
	}
	if code := addSourceLabels(ctx, a, s, *fromReviews, exps, *seed); code != exitOK {
		return code
	}
	opts := calibrateOptions{yes: *yes, thresh: th, judgeCfg: func() (graders.JudgeConfig, error) {
		live, err := newApp(ctx, *config, appOptions{gateway: true})
		if err != nil {
			return graders.JudgeConfig{}, err
		}
		return graders.JudgeConfig{Gateway: live.reg.Gateway, URL: live.cfg.Gateway.URL, Price: graders.PricesFrom(live.prices.Lookup),
			PriceRef: live.prices.Ref(), Logger: live.logger}, nil
	}}
	if _, err := calibrateSuite(ctx, a, s, opts, os.Stdin, os.Stdout); err != nil {
		return fail("judge calibrate: %v", err)
	}
	return exitOK
}

// addSourceLabels adds the review and seed labels a command asked for.
func addSourceLabels(ctx context.Context, a *app, s *schemas.Suite, fromReviews bool, exps []string, seed bool) int {
	if fromReviews {
		for _, e := range exps {
			items, skipped, err := reviewLabels(ctx, a.store, e, s)
			if err != nil {
				return fail("labels from reviews of %s: %v", e, err)
			}
			n, err := calibration.Append(s.Root, items)
			if err != nil {
				return fail("labels from reviews of %s: %v", e, err)
			}
			fmt.Printf("experiment %s: %d reviewed run(s) labelled, %d new label row(s)\n", e, len(items), n)
			for _, sk := range skipped {
				fmt.Printf("  skipped: %s\n", sk)
			}
		}
	}
	if seed {
		n, err := calibration.Append(s.Root, seedLabels(s))
		if err != nil {
			return fail("seed labels: %v", err)
		}
		fmt.Printf("admission controls: %d new seed row(s) (weak: never counted as human labels)\n", n)
	}
	return exitOK
}

func runJudgeLabel(args []string) int {
	fs := flag.NewFlagSet("judge label", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	task := fs.String("task", "", "task id")
	answer := fs.String("answer", "", "the answer text")
	answerFile := fs.String("answer-file", "", "read the answer text from this file")
	runID := fs.String("run", "", "take the task and answer from this sealed run")
	label := fs.String("label", "", "correct or incorrect")
	labeller := fs.String("labeller", "", "your name (required; self-declared, there is no login)")
	note := fs.String("note", "", "why, in plain text")
	importFile := fs.String("import", "", "import labels from a CSV (header task,answer,label,labeller[,note,ref]) or JSONL file")
	seed := fs.Bool("seed-controls", false, "add the suite's expected and wrong answers as weak seed items")
	id, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || id == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, judgeUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	j := judgeSvc{a: a}
	s, err := j.load(ctx, id)
	if err != nil {
		return fail("judge label: %v", err)
	}
	switch {
	case *seed:
		return addSourceLabels(ctx, a, s, false, nil, true)
	case *importFile != "":
		raw, err := os.ReadFile(*importFile)
		if err != nil {
			return fail("judge label: %v", err)
		}
		var items []schemas.CalibrationItem
		if strings.EqualFold(filepath.Ext(*importFile), ".csv") {
			items, err = calibration.ParseCSV(raw, *importFile)
		} else {
			items, err = calibration.ParseJSONL(raw, *importFile, true)
		}
		if err != nil {
			return fail("judge label: %v", err)
		}
		for _, it := range items {
			if s.TaskByID(it.Task) == nil {
				return fail("judge label: task %s is not in suite %s", it.Task, s.ID)
			}
		}
		n, err := calibration.Append(s.Root, items)
		if err != nil {
			return fail("judge label: %v", err)
		}
		fmt.Printf("%d row(s) read, %d new label row(s) in %s\n", len(items), n, calibration.LabelsPath(s.Root))
		return exitOK
	}
	text := *answer
	switch {
	case *runID != "":
		t, ans, err := runAnswer(ctx, a.store, *runID)
		if err != nil {
			return fail("judge label: %v", err)
		}
		if *task == "" {
			*task = t
		}
		text = ans
	case *answerFile != "":
		b, err := os.ReadFile(*answerFile)
		if err != nil {
			return fail("judge label: %v", err)
		}
		text = string(b)
	}
	if *task == "" || *label == "" {
		fmt.Fprintln(os.Stderr, judgeUsage)
		return exitUsage
	}
	if s.TaskByID(*task) == nil {
		return fail("judge label: task %s is not in suite %s", *task, s.ID)
	}
	it, err := calibration.NewItem(schemas.CalibrationLabelRequest{Task: *task, Answer: text, Label: *label, Labeller: *labeller, Note: *note, Ref: *runID},
		schemas.CalibrationSourceLabel, time.Now())
	if err != nil {
		return fail("judge label: %v", err)
	}
	n, err := calibration.Append(s.Root, []schemas.CalibrationItem{it})
	if err != nil {
		return fail("judge label: %v", err)
	}
	if n == 0 {
		fmt.Printf("item %s: the same label is already there\n", it.ID)
		return exitOK
	}
	fmt.Printf("item %s labelled %s by %s\n", it.ID, it.Label, it.Labeller)
	return exitOK
}

// runAnswer returns a sealed run's task and answer.
func runAnswer(ctx context.Context, st *store.FSStore, runID string) (string, string, error) {
	expID, err := st.RunExp(runID)
	if err != nil {
		return "", "", err
	}
	runs, err := st.SealedRuns(ctx, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return "", "", err
	}
	for _, sr := range runs {
		if sr.Run.RunID != runID {
			continue
		}
		ans, problem, err := graders.ReadAnswer(sr.Dir)
		if err != nil {
			return "", "", err
		}
		if problem != "" {
			return "", "", errors.New(problem)
		}
		return sr.Slot.Task, ans, nil
	}
	return "", "", fmt.Errorf("run %s is not a sealed run of %s", runID, expID)
}

func runJudgeStatus(args []string) int {
	fs := flag.NewFlagSet("judge status", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	policyFile := fs.String("policy", "", "release policy file whose judge_calibration thresholds apply")
	asJSON := fs.Bool("json", false, "print the status as JSON")
	id, rest := splitRunArg(args)
	if err := fs.Parse(rest); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, judgeUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	th, err := thresholdsFrom(*policyFile)
	if err != nil {
		return fail("judge status: %v", err)
	}
	j := judgeSvc{a: a}
	ids := []string{id}
	if id == "" {
		if ids, err = (serveSuites{root: a.cfg.Path(a.cfg.BenchmarksRoot)}).SuiteIDs(ctx); err != nil {
			return fail("judge status: %v", err)
		}
	}
	var all []*schemas.CalibrationStatus
	for _, sid := range ids {
		s, err := j.load(ctx, sid)
		if err != nil {
			if id != "" {
				return fail("judge status: %v", err)
			}
			continue
		}
		if id == "" && (s.Grading != schemas.GradingJudged || s.Judge == nil) {
			continue
		}
		st, err := j.status(s, th)
		if err != nil {
			return fail("judge status %s: %v", sid, err)
		}
		all = append(all, st)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(all); err != nil {
			return fail("%v", err)
		}
		return exitOK
	}
	if len(all) == 0 {
		fmt.Println("no judged suite")
	}
	for _, st := range all {
		writeStatus(os.Stdout, st)
	}
	return exitOK
}

func writeStatus(w io.Writer, st *schemas.CalibrationStatus) {
	fmt.Fprintf(w, "suite %s: %s\n", st.Suite, st.State)
	if st.Judged {
		fmt.Fprintf(w, "  judge %s/%s, prompt %s", st.Provider, st.Model, st.PromptVersion)
		if len(st.PromptSHA256) >= 12 {
			fmt.Fprintf(w, " (%s)", st.PromptSHA256[:12])
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "  %s\n", st.Detail)
	fmt.Fprintf(w, "  labels: %d rows, %d human-labelled items, %d seeds, %d disputed; %d record(s)\n", st.Labels, st.HumanLabels, st.SeedItems, st.Disputed, st.Records)
	if r := st.Latest; r != nil {
		fmt.Fprintf(w, "  latest record %s on %s, set %s\n", r.RecordID, r.CreatedAt.Format("2006-01-02"), short(r.SetSHA256))
	}
	for _, r := range st.Reminders {
		fmt.Fprintf(w, "  reminder: %s\n", r)
	}
	for _, p := range st.Problems {
		fmt.Fprintf(w, "  problem: %s\n", p)
	}
}

// writeRecordSummary prints a record's statistics.
func writeRecordSummary(w io.Writer, rec *schemas.CalibrationRecord, th schemas.JudgeCalibrationThresholds) {
	h := rec.Human
	fmt.Fprintf(w, "human labels %d: agreement %s, TPR %s, TNR %s, false-positive rate %s", rec.HumanLabels,
		rateLine(h.Agreement), rateLine(h.TPR), rateLine(h.TNR), rateLine(h.FPR))
	if h.Kappa != nil {
		fmt.Fprintf(w, ", kappa %.3f", *h.Kappa)
	}
	fmt.Fprintf(w, ", undecided %d\n", h.Undecided)
	if rec.Seed != nil {
		fmt.Fprintf(w, "seeds %d (weak): agreement %s\n", rec.SeedItems, rateLine(rec.Seed.Agreement))
	}
	for _, c := range rec.Categories {
		fmt.Fprintf(w, "  category %s: agreement %s, TNR %s\n", c.Category, rateLine(c.Stats.Agreement), rateLine(c.Stats.TNR))
	}
	c := rec.Consistency
	if c.Votes > 1 {
		fmt.Fprintf(w, "consistency: unanimous votes %s; ", rateLine(c.UnanimousItems))
	}
	fmt.Fprintf(w, "same verdict in two passes %s\n", rateLine(c.RepeatAgreement))
	for _, p := range rec.Perturbations {
		fmt.Fprintf(w, "perturbation %s: verdict flipped %s\n", p.Name, rateLine(p.Flips))
	}
	if v, ok := rec.USD.Float(); ok {
		fmt.Fprintf(w, "cost: $%.4f over %d calls\n", v, rec.Calls)
	} else {
		fmt.Fprintf(w, "cost: unknown over %d calls\n", rec.Calls)
	}
	fmt.Fprintf(w, "thresholds (%s): agreement lower bound >= %.2f, TNR lower bound >= %.2f, %d+ human labels\n", th.Source, th.MinAgreement, th.MinTNR, th.MinLabels)
}

func rateLine(r schemas.CalibrationRate) string {
	if r.Value == nil {
		return fmt.Sprintf("n/a (%d/%d)", r.K, r.N)
	}
	return fmt.Sprintf("%.3f [%.3f, %.3f] (%d/%d)", *r.Value, *r.Lo, *r.Hi, r.K, r.N)
}

// judgeCalibration adds the judge_calibration instrument to a judged
// experiment's analysis (calibration.Apply), reading the suite's records
// and labels, the thresholds of the release policy in force and the
// experiment's reviews. Any other experiment is left untouched.
func (a *app) judgeCalibration(ctx context.Context, m *schemas.Manifest, an *schemas.Analysis, runs []schemas.SealedRun, policyOverride string) {
	if !calibration.Judged(m, runs) {
		return
	}
	in := calibration.Input{Manifest: m, Runs: runs, Now: time.Now().UTC(), Reviews: map[string][]schemas.Review{}}
	if root, err := (judgeSvc{a: a}).suiteRoot(m.SuiteID); err == nil {
		if recs, err := calibration.ReadRecords(root); err == nil {
			in.Records = recs
		} else {
			a.logger.Warn("calibration records could not be read", "suite", m.SuiteID, "error", err.Error())
		}
		in.SetSHA256, _ = utils.SHA256File(calibration.LabelsPath(root))
	}
	path := policyOverride
	if path == policyNone {
		path = ""
	} else if path == "" && m.Experiment.Policy != "" {
		path = a.cfg.Path(m.Experiment.Policy)
	}
	if path != "" {
		if p, _, err := policy.Load(path); err == nil {
			in.Policy, in.PolicySource = p.JudgeCalibration, path
		}
	}
	if set, err := a.store.Reviews(ctx, m.ExpID); err == nil {
		for _, r := range set.Valid {
			in.Reviews[r.RunID] = append(in.Reviews[r.RunID], r)
		}
	}
	calibration.Apply(an, in)
}

// judgeCalibrationSection inserts the report's Judge calibration section.
func (a *app) judgeCalibrationSection(an *schemas.Analysis, report string) string {
	return insertSection(report, calibration.Section(an.JudgeCalibration))
}
