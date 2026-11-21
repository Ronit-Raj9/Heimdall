package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/reporters"
	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	register("plan", "show what an experiment would run; nothing runs", runPlan)
	register("run", "plan, freeze and run an experiment, then write its verdict", runRun)
	register("check", "run (or re-analyze) an experiment and exit non-zero on a listed verdict", runCheck)
}

func runPlan(args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	allowUnpriced := fs.Bool("allow-unpriced", false, "plan matrix models the price snapshot does not list") // Forecast and matrix area (estimate.go)
	// Suite health area: -include-quarantined (suitehealth.go).
	fs.BoolVar(&includeQuarantined, "include-quarantined", false, "let an explicit task list name a quarantined task")
	pos, err := parseInterspersed(fs, args) // flags may follow the file (7-C-01)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench plan [-config deploy/bench.yaml] [-allow-unpriced] experiments/<file>.yaml")
		return exitUsage
	}
	ctx := context.Background()
	// Read the experiment first: building the app sets the gateway's
	// providers and key slots up, which a missing or broken experiment
	// file must not trigger (audit 5-B-08).
	exp, err := loadExperiment(firstArg(pos))
	if err != nil {
		return fail("%v", err)
	}
	// The planner runs the gateway preflight, so plan needs the live gateway.
	a, err := newApp(ctx, *config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	allowUnpricedModels(exp, *allowUnpriced) // Forecast and matrix area (estimate.go)
	if err := a.validatePolicy(exp); err != nil {
		return fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fail("plan: %v", err)
	}
	printPlan(m)
	printAdaptivePlan(m)    // W7 adaptive trials and suite tiers: required and optional slots, tier (adaptivereport.go)
	a.printEstimate(ctx, m) // Forecast and matrix area: cost and time forecast from sealed history (estimate.go)
	return exitOK
}

// printPlan prints slot counts per condition and arm.
func printPlan(m *schemas.Manifest) {
	counts := map[string]map[string]int{}
	compat := 0
	for _, s := range m.Slots {
		if !s.Counted {
			compat++
			continue
		}
		if counts[s.Condition] == nil {
			counts[s.Condition] = map[string]int{}
		}
		counts[s.Condition][s.Arm]++
	}
	fmt.Printf("experiment %s  manifest %s\n", m.ExpID, short(m.Hash))
	total := 0
	for _, c := range m.Experiment.Conditions {
		n := 0
		for _, v := range counts[c.ID] {
			n += v
		}
		total += n
		fmt.Printf("  %-16s %3d slots  %s\n", c.ID, n, armCounts(counts[c.ID]))
	}
	fmt.Printf("  counted %d, compatibility %d, total %d agent episodes\n", total, compat, total+compat)
}

// armCounts formats slots per arm in arm order, "arm A 2, arm B 2".
func armCounts(counts map[string]int) string {
	arms := make([]string, 0, len(counts))
	for arm := range counts {
		arms = append(arms, arm)
	}
	sort.Strings(arms)
	parts := make([]string, 0, len(arms))
	for _, arm := range arms {
		parts = append(parts, fmt.Sprintf("arm %s %d", arm, counts[arm]))
	}
	return strings.Join(parts, ", ")
}

// progressLine is one line of run progress: time, event kind, slot, then the
// stage, run and outcome when the event has them, so a "stage" line says
// which stage started or finished.
func progressLine(ev engine.Event) string {
	line := fmt.Sprintf("%s %-14s %s", ev.At.Format("15:04:05"), ev.Kind, ev.SlotID)
	if ev.Stage != "" {
		line += " " + string(ev.Stage)
	}
	if ev.RunID != "" {
		line += " " + ev.RunID
	}
	if ev.Outcome != "" {
		line += " " + string(ev.Outcome)
	}
	if ev.Message != "" {
		line += " " + ev.Message
	}
	return line
}

func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	resume := fs.String("resume", "", "resume an existing experiment id instead of planning a new one")
	allowDrift := fs.Bool("allow-version-drift", false, "resume even when part versions or the price snapshot changed since the freeze; recorded in the experiment's resumes.jsonl")
	concurrency := fs.Int("concurrency", 0, "override the experiment's concurrency")
	debug := fs.Bool("debug", false, "debug logging")
	// Suite health area: -include-quarantined (suitehealth.go).
	fs.BoolVar(&includeQuarantined, "include-quarantined", false, "let an explicit task list name a quarantined task")
	pos, err := parseInterspersed(fs, args) // flags may follow the file (7-C-01)
	if err != nil || len(pos) > 1 || (len(pos) != 1 && *resume == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench run [-config deploy/bench.yaml] [-concurrency n] experiments/<file>.yaml | -resume <exp_id> [-allow-version-drift]")
		return exitUsage
	}
	if *resume == "" { // 5-B-08: a bad experiment file fails before the gateway is set up
		if _, err := loadExperiment(firstArg(pos)); err != nil {
			return fail("%v", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: true, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	m, code := a.manifestFor(ctx, firstArg(pos), *resume, *allowDrift)
	if m == nil {
		return code
	}
	// manifestFor has already frozen the experiment in the store, so the
	// scheduler always runs in resume mode: it runs every slot without a
	// counted sealed run.
	if code := a.execute(ctx, m, *concurrency); code != exitOK {
		return code
	}
	an, report, err := a.analyze(context.WithoutCancel(ctx), m.ExpID, "")
	if err != nil {
		return fail("analyze %s: %v", m.ExpID, err)
	}
	fmt.Print(string(report))
	fmt.Printf("\nverdict: %s, action: %s (analysis %s)\n", an.Verdict.Decision, an.Verdict.Action, an.AnalysisID)
	printPolicy(an.Policy)
	return exitOK
}

// manifestFor plans and creates a new experiment, or loads one to resume.
// A resume is refused when the bench changed since the freeze (resumeDrift)
// unless allowDrift is set; an allowed drift is recorded beside the manifest.
func (a *app) manifestFor(ctx context.Context, path, resume string, allowDrift bool) (*schemas.Manifest, int) {
	if resume != "" {
		m, err := a.store.LoadManifest(ctx, resume)
		if err != nil {
			return nil, fail("load %s: %v", resume, err)
		}
		d, err := a.resumeDrift(m)
		if err != nil {
			return nil, fail("resume %s: %v", resume, err)
		}
		for _, p := range d.Added {
			a.logger.Warn("part registered after the freeze; the experiment's slots do not name it", "experiment", resume, "part", p)
		}
		if !d.refuses() {
			return m, exitOK
		}
		if !allowDrift {
			return nil, fail("resume %s: the bench changed since the experiment was frozen, so new runs would not be comparable with the sealed ones:\n  %s\nresume with -allow-version-drift to run anyway (recorded in %s), or start a new experiment",
				resume, strings.Join(d.lines(), "\n  "), resumesFile)
		}
		if err := a.recordDrift(m.ExpID, d); err != nil {
			return nil, fail("resume %s: record version drift: %v", resume, err)
		}
		a.logger.Warn("resuming across a version drift (-allow-version-drift)", "experiment", resume, "drift", strings.Join(d.lines(), "; "))
		return m, exitOK
	}
	exp, err := loadExperiment(path)
	if err != nil {
		return nil, fail("%v", err)
	}
	if err := a.validatePolicy(exp); err != nil {
		return nil, fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return nil, fail("plan: %v", err)
	}
	if err := a.store.CreateExperiment(ctx, m); err != nil {
		return nil, fail("freeze %s: %v", m.ExpID, err)
	}
	printPlan(m)
	return m, exitOK
}

// execute runs every slot and prints progress lines.
// runContext is the scheduler context for a CLI run or check: an interrupt
// or SIGTERM ending ctx is an owner stop (engine.ErrRunCancelled), as in the
// hosted launcher, so an in-flight run seals as aborted_infra and never
// counts, and run -resume runs its slot again (QC2-06-05).
func runContext(ctx context.Context) (context.Context, func()) {
	return engine.CancelContext(ctx)
}

func (a *app) execute(ctx context.Context, m *schemas.Manifest, concurrency int) int {
	events := make(chan engine.Event, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Kind == "" {
				continue
			}
			fmt.Fprintln(os.Stderr, progressLine(ev))
			indexOnSeal(ctx, a, m.ExpID, ev) // Index and trends area: results index update when a run seals (index.go)
		}
	}()
	janitor := engine.NewJanitor(a.reg.Gateway, a.docker, a.logger)
	sched := engine.NewScheduler(a.reg, a.cfg.Root, a.logger, janitor)
	rctx, stopRun := runContext(ctx)
	defer stopRun()
	sum, err := sched.Run(rctx, m, engine.RunOptions{Resume: true, Concurrency: concurrency, Events: events})
	close(events)
	<-done
	if sum != nil {
		if a.cfg.Server.Mode == schemas.ModeSynthetic {
			sum.SpendBasis = engine.SpendSimulated // scripted model answers (BENCH_SERVER_MODE=synthetic)
		}
		b, _ := json.Marshal(sum)
		fmt.Fprintf(os.Stderr, "summary: %s\n", b)
		fmt.Fprintln(os.Stderr, spendNote(sum))
	}
	if err != nil {
		return fail("run %s: %v", m.ExpID, err)
	}
	return exitOK
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "re-analyze an existing experiment id instead of running a file")
	failOn := fs.String("fail-on", "regressed,invalid", "comma-separated verdicts that fail the check")
	out := fs.String("out", "report.md", "markdown report path")
	policyFile := fs.String("policy", "", "release policy file; overrides the one the experiment froze (its frozen hash check does not apply to an override); none turns policy evaluation off")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, reviewed (human reviews applied, see docs/human-review.md), or a regrade id (from regate or a regrade) to analyze with")
	pos, err := parseInterspersed(fs, args) // flags may follow the file (7-C-01)
	if err != nil || len(pos) > 1 || (len(pos) != 1 && *exp == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench check [-fail-on regressed,invalid] [-policy policies/default.yaml] [-grade-source original|reviewed|<regrade_id>] [-out report.md] experiments/<file>.yaml | -exp <exp_id>")
		fmt.Fprintln(os.Stderr, "exit: 0 ok, 1 regressed, 2 inconclusive, 3 invalid (each only when listed in -fail-on), 4 infrastructure error, 5 blocked by the release policy")
		return exitUsage
	}
	failDecisions, err := parseDecisions(*failOn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return exitUsage
	}
	if *policyFile != "" && *policyFile != policyNone {
		// Fail before a long run, not after it.
		if _, _, err := policy.Load(*policyFile); err != nil {
			return fail("%v", err)
		}
	}
	if *exp == "" { // 5-B-08: a bad experiment file fails before the gateway is set up
		if _, err := loadExperiment(firstArg(pos)); err != nil {
			return fail("%v", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: *exp == ""})
	if err != nil {
		return fail("%v", err)
	}
	expID := *exp
	if expID == "" {
		m, code := a.manifestFor(ctx, firstArg(pos), "", false)
		if m == nil {
			return code
		}
		if code := a.execute(ctx, m, 0); code != exitOK {
			return code
		}
		expID = m.ExpID
	}
	an, report, err := a.analyzeWith(context.WithoutCancel(ctx), expID, *gradeSource, *policyFile)
	if err != nil {
		return fail("analyze %s: %v", expID, err)
	}
	if err := os.WriteFile(*out, report, 0o644); err != nil {
		return fail("write %s: %v", *out, err)
	}
	// The JSON beside the markdown is part of the check's output (ci card
	// reads it): a failed write fails the check (audit 5-B-09).
	b, err := json.MarshalIndent(an, "", "  ")
	if err != nil {
		return fail("encode %s: %v", trimExt(*out)+".json", err)
	}
	if err := os.WriteFile(trimExt(*out)+".json", b, 0o644); err != nil {
		return fail("write %s: %v", trimExt(*out)+".json", err)
	}
	fmt.Printf("verdict: %s, action: %s\n", an.Verdict.Decision, an.Verdict.Action)
	printPolicy(an.Policy)
	return checkExitCode(an, failDecisions)
}

// checkExitCode is the exit code of `check`. A release policy that blocks is
// checked first and exits 5 (policy.ExitBlock) whatever the verdict, because
// a policy is a rule the team set for this release; otherwise the verdict
// codes apply: 0 when the decision is not in failOn, else 1 regressed, 2
// inconclusive, 3 invalid. 4 stays the infrastructure error. A warn decision
// never changes the exit code.
func checkExitCode(an *schemas.Analysis, failOn []schemas.Decision) int {
	if an.Policy != nil && an.Policy.Decision == schemas.PolicyBlock {
		return policy.ExitBlock
	}
	return reporters.ExitCode(an.Verdict.Decision, failOn)
}

// printPolicy prints the release policy decision and the checks that are not
// a pass.
func printPolicy(r *schemas.PolicyResult) {
	if r == nil {
		fmt.Println("policy: none")
		return
	}
	counts := map[schemas.PolicyStatus]int{}
	for _, c := range r.Checks {
		counts[c.Status]++
	}
	fmt.Printf("policy %s (%s): %s, %d checks: %d pass, %d fail, %d unknown\n", r.PolicyName, short(r.PolicyHash),
		r.Decision, len(r.Checks), counts[schemas.PolicyPass], counts[schemas.PolicyFail], counts[schemas.PolicyUnknown])
	for _, c := range r.Checks {
		if c.Status == schemas.PolicyPass {
			continue
		}
		tag := "warn"
		if c.Blocking {
			tag = "block"
		}
		fmt.Printf("  %-7s %-5s %s: %s\n", c.Status, tag, c.ID, c.Reason)
	}
}

// resumesFile is the experiment-level log of resumes that crossed a version
// drift, one JSON object per line, beside manifest.json and outside every
// sealed run.
const resumesFile = "resumes.jsonl"

// versionDrift is what changed between an experiment's freeze and a resume.
type versionDrift struct {
	Changed []string `json:"changed,omitempty"` // parts at another version now, or no longer registered
	Added   []string `json:"added,omitempty"`   // parts registered after the freeze; listed, never refused
	Prices  string   `json:"prices,omitempty"`  // the price snapshot of the sealed runs differs from the current one
}

// refuses reports whether the drift makes new runs incomparable: a part the
// manifest froze changed or is gone, or new runs would be priced with another
// snapshot. A part added since the freeze is not used by the frozen slots.
func (d versionDrift) refuses() bool { return len(d.Changed) > 0 || d.Prices != "" }

// lines is the drift as one line per difference.
func (d versionDrift) lines() []string {
	out := append([]string(nil), d.Changed...)
	if d.Prices != "" {
		out = append(out, d.Prices)
	}
	return out
}

// resumeDrift compares the part versions this binary would run with
// (engine.PartVersions) with the ones the manifest froze, and the price
// snapshot it would cost new runs with against the price_ref the sealed
// runs' cost rows carry. The fault proxy runs as its own service and is not
// in the part list, so it is not compared.
func (a *app) resumeDrift(m *schemas.Manifest) (versionDrift, error) {
	var d versionDrift
	d.Changed, d.Added = partDrift(m.Parts, engine.PartVersions(a.reg))
	if a.prices == nil {
		return d, nil
	}
	refs, err := sealedPriceRefs(filepath.Join(a.store.Root(), m.ExpID))
	if err != nil {
		return d, err
	}
	now := a.prices.Ref()
	if len(refs) > 1 || (len(refs) == 1 && refs[0] != now) {
		d.Prices = fmt.Sprintf("price snapshot: sealed runs were costed with %s, this bench costs with %s", strings.Join(refs, ", "), now)
	}
	return d, nil
}

// partDrift returns the frozen parts whose version changed or that are no
// longer registered, and the parts registered since, each sorted.
func partDrift(frozen, now map[string]string) (changed, added []string) {
	for part, v := range frozen {
		switch cur, ok := now[part]; {
		case !ok:
			changed = append(changed, fmt.Sprintf("%s: frozen at %s, no longer registered", part, v))
		case cur != v:
			changed = append(changed, fmt.Sprintf("%s: frozen at %s, now %s", part, v, cur))
		}
	}
	for part := range now {
		if _, ok := frozen[part]; !ok {
			added = append(added, part)
		}
	}
	sort.Strings(changed)
	sort.Strings(added)
	return changed, added
}

// sealedPriceRefs returns the distinct price_ref values in the cost rows of
// an experiment's sealed runs, sorted. A run with no cost.json (or no rows)
// adds none.
func sealedPriceRefs(expDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(expDir, "slots", "*", "runs", "*", "cost.json"))
	if err != nil {
		return nil, fmt.Errorf("list cost rows: %w", err)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(filepath.Dir(p), "seal.json")); err != nil {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var rows []struct {
			PriceRef string `json:"price_ref"`
		}
		if err := json.Unmarshal(b, &rows); err != nil {
			return nil, fmt.Errorf("decode %s: %w", p, err)
		}
		for _, r := range rows {
			if r.PriceRef != "" {
				seen[r.PriceRef] = true
			}
		}
	}
	refs := make([]string, 0, len(seen))
	for r := range seen {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	return refs, nil
}

// recordDrift appends an allowed drift to the experiment's resumes.jsonl, so
// every later reader can see that the runs after this point ran on a bench
// that differs from the frozen one.
func (a *app) recordDrift(expID string, d versionDrift) error {
	rec := struct {
		ResumedAt  time.Time `json:"resumed_at"`
		AllowDrift bool      `json:"allow_version_drift"`
		versionDrift
	}{ResumedAt: time.Now().UTC(), AllowDrift: true, versionDrift: d}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(a.store.Root(), expID, resumesFile), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// spendNote says what the summary's spent_usd is, so it is not read as a
// provider bill.
func spendNote(sum *engine.Summary) string {
	if sum.SpendBasis == engine.SpendSimulated {
		return fmt.Sprintf("spend: %.4f USD simulated (scripted model answers priced at the pinned table; no provider was charged)", sum.SpentUSD)
	}
	return fmt.Sprintf("spend: %.4f USD modeled (recorded usage priced at the pinned table, unknown costs at the per-run estimate; not a provider bill)", sum.SpentUSD)
}
