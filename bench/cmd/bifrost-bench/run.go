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
	a, err := newApp(ctx, *config, appOptions{gateway: false})
	if err != nil {
		return fail("%v", err)
	}
	exp, err := loadExperiment(fs.Arg(0))
	if err != nil {
		return fail("%v", err)
	}
	m, err := engine.NewPlanner(a.reg, a.cfg.Root, a.logger).Plan(ctx, exp)
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
	if code := a.execute(ctx, m, *resume != "", *concurrency); code != exitOK {
		return code
	}
	an, report, err := a.analyze(context.WithoutCancel(ctx), m.ExpID, "")
	if err != nil {
		return fail("analyze %s: %v", m.ExpID, err)
	}
	fmt.Print(string(report))
	fmt.Printf("\nverdict: %s, action: %s (analysis %s)\n", an.Verdict.Decision, an.Verdict.Action, an.AnalysisID)
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
	m, err := engine.NewPlanner(a.reg, a.cfg.Root, a.logger).Plan(ctx, exp)
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
func (a *app) execute(ctx context.Context, m *schemas.Manifest, resume bool, concurrency int) int {
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
	sum, err := sched.Run(ctx, m, engine.RunOptions{Resume: resume, Concurrency: concurrency, Events: events})
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
	if err := fs.Parse(args); err != nil || (fs.NArg() != 1 && *exp == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench check [-fail-on regressed,invalid] [-out report.md] experiments/<file>.yaml | -exp <exp_id>")
		return exitUsage
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
		if code := a.execute(ctx, m, false, 0); code != exitOK {
			return code
		}
		expID = m.ExpID
	}
	an, report, err := a.analyze(context.WithoutCancel(ctx), expID, "")
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
	return reporters.ExitCode(an.Verdict.Decision, parseDecisions(*failOn))
}
