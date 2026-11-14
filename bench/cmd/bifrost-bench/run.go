package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

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
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench plan [-config deploy/bench.yaml] experiments/<file>.yaml")
		return exitUsage
	}
	ctx := context.Background()
	// The planner runs the gateway preflight, so plan needs the live gateway.
	a, err := newApp(ctx, *config, appOptions{gateway: true})
	if err != nil {
		return fail("%v", err)
	}
	exp, err := loadExperiment(fs.Arg(0))
	if err != nil {
		return fail("%v", err)
	}
	if err := a.validatePolicy(exp); err != nil {
		return fail("%v", err)
	}
	m, err := a.newPlanner().Plan(ctx, exp)
	if err != nil {
		return fail("plan: %v", err)
	}
	printPlan(m)
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
		fmt.Printf("  %-16s %3d slots  %v\n", c.ID, n, counts[c.ID])
	}
	fmt.Printf("  counted %d, compatibility %d, total %d agent episodes\n", total, compat, total+compat)
}

func runRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	resume := fs.String("resume", "", "resume an existing experiment id instead of planning a new one")
	concurrency := fs.Int("concurrency", 0, "override the experiment's concurrency")
	debug := fs.Bool("debug", false, "debug logging")
	if err := fs.Parse(args); err != nil || (fs.NArg() != 1 && *resume == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench run [-config deploy/bench.yaml] [-concurrency n] experiments/<file>.yaml | -resume <exp_id>")
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := newApp(ctx, *config, appOptions{gateway: true, debug: *debug})
	if err != nil {
		return fail("%v", err)
	}
	m, code := a.manifestFor(ctx, fs.Arg(0), *resume)
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
func (a *app) manifestFor(ctx context.Context, path, resume string) (*schemas.Manifest, int) {
	if resume != "" {
		m, err := a.store.LoadManifest(ctx, resume)
		if err != nil {
			return nil, fail("load %s: %v", resume, err)
		}
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
func (a *app) execute(ctx context.Context, m *schemas.Manifest, concurrency int) int {
	events := make(chan engine.Event, 256)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Kind == "" {
				continue
			}
			line := fmt.Sprintf("%s %-14s %s", ev.At.Format("15:04:05"), ev.Kind, ev.SlotID)
			if ev.Outcome != "" {
				line += " " + string(ev.Outcome)
			}
			if ev.Message != "" {
				line += " " + ev.Message
			}
			fmt.Fprintln(os.Stderr, line)
		}
	}()
	janitor := engine.NewJanitor(a.reg.Gateway, a.docker, a.logger)
	sched := engine.NewScheduler(a.reg, a.cfg.Root, a.logger, janitor)
	sum, err := sched.Run(ctx, m, engine.RunOptions{Resume: true, Concurrency: concurrency, Events: events})
	close(events)
	<-done
	if sum != nil {
		b, _ := json.Marshal(sum)
		fmt.Fprintf(os.Stderr, "summary: %s\n", b)
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
	policyFile := fs.String("policy", "", "release policy file; overrides the one the experiment froze")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, or a regrade id (from regate or a regrade) to analyze with")
	if err := fs.Parse(args); err != nil || (fs.NArg() != 1 && *exp == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench check [-fail-on regressed,invalid] [-policy policies/default.yaml] [-grade-source original|<regrade_id>] [-out report.md] experiments/<file>.yaml | -exp <exp_id>")
		fmt.Fprintln(os.Stderr, "exit: 0 ok, 1 regressed, 2 inconclusive, 3 invalid (each only when listed in -fail-on), 4 infrastructure error, 5 blocked by the release policy")
		return exitUsage
	}
	if *policyFile != "" {
		// Fail before a long run, not after it.
		if _, _, err := policy.Load(*policyFile); err != nil {
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
		m, code := a.manifestFor(ctx, fs.Arg(0), "")
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
	if b, err := json.MarshalIndent(an, "", "  "); err == nil {
		_ = os.WriteFile(trimExt(*out)+".json", b, 0o644)
	}
	fmt.Printf("verdict: %s, action: %s\n", an.Verdict.Decision, an.Verdict.Action)
	printPolicy(an.Policy)
	return checkExitCode(an, parseDecisions(*failOn))
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
