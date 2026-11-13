package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func init() {
	register("suite", "lint, lock or admit a benchmark pack: suite lint|lock|admit <id>", runSuite)
}

func runSuite(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench suite lint|lock [-root benchmarks] <suite id>\n       bifrost-bench suite admit [-config deploy/bench.yaml] [-task t01] <suite id>")
		return exitUsage
	}
	sub := args[0]
	if sub == "admit" {
		return runAdmit(args[1:])
	}
	fs := flag.NewFlagSet("suite "+sub, flag.ContinueOnError)
	root := fs.String("root", "benchmarks", "benchmarks folder")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 1 {
		return exitUsage
	}
	s, err := loadSuite(context.Background(), *root, fs.Arg(0))
	if err != nil {
		return fail("load suite %s: %v", fs.Arg(0), err)
	}
	switch sub {
	case "lint":
		problems := suite.Lint(s)
		for _, p := range problems {
			fmt.Printf("%-7s %s: %s\n", p.Severity, p.Path, p.Message)
		}
		fmt.Printf("%s@%s: %d tasks, %d probes, %d problems\n", s.ID, s.Version, len(s.Tasks), len(s.Probes), len(problems))
		for _, p := range problems {
			if p.Severity == "error" {
				return 1
			}
		}
		return exitOK
	case "lock":
		lock, err := suite.Lock(s, time.Now().UTC())
		if err != nil {
			return fail("lock: %v", err)
		}
		path := filepath.Join(s.Root, "suite.lock")
		if _, err := suite.WriteLock(path, lock); err != nil {
			return fail("write %s: %v", path, err)
		}
		fmt.Printf("wrote %s (%d tasks, %d probes)\n", path, len(lock.Tasks), len(lock.Probes))
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown suite subcommand %q\n", sub)
		return exitUsage
	}
}

// loadSuite loads a suite with the first registered loader.
func loadSuite(ctx context.Context, root, ref string) (*schemas.Suite, error) {
	loaders := suite.Builtin(root)
	if len(loaders) == 0 {
		return nil, fmt.Errorf("no suite loader registered")
	}
	return loaders[0].Load(ctx, ref)
}

// runAdmit runs the admission controls for every task (or one) with the real
// environment and grader, records each result in the suite lock, and writes
// the lock. A task whose controls fail stays a draft and cannot be planned.
func runAdmit(args []string) int {
	fs := flag.NewFlagSet("suite admit", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	only := fs.String("task", "", "admit only this task")
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), fs.Arg(0))
	if err != nil {
		return fail("load suite %s: %v", fs.Arg(0), err)
	}
	lock, err := suite.Lock(s, time.Now().UTC())
	if err != nil {
		return fail("lock: %v", err)
	}
	deps := suite.AdmitDeps{Logger: a.logger}
	for _, e := range a.reg.Environments {
		if e.GetName() == s.Environment {
			deps.Env = e
		}
	}
	for _, g := range a.reg.Graders {
		if g.GetName() == s.Grader {
			deps.Grader = g
		}
	}
	failed := 0
	for _, t := range s.Tasks {
		if *only != "" && t.ID != *only {
			continue
		}
		rec, err := suite.Admit(ctx, deps, s, t)
		if err != nil {
			return fail("admit %s: %v", t.ID, err)
		}
		if err := suite.RecordAdmission(lock, t.ID, rec); err != nil {
			return fail("%v", err)
		}
		status := "admitted"
		if !rec.Admitted {
			status = "NOT ADMITTED"
			failed++
		}
		fmt.Printf("%-4s %s\n", t.ID, status)
		for _, c := range rec.Checks {
			mark := "ok  "
			if !c.Passed {
				mark = "FAIL"
			}
			fmt.Printf("     %s %-40s want %-5s got %s\n", mark, c.Name, c.Want, c.Got)
		}
	}
	path := filepath.Join(s.Root, "suite.lock")
	if _, err := suite.WriteLock(path, lock); err != nil {
		return fail("write %s: %v", path, err)
	}
	fmt.Printf("wrote %s\n", path)
	if failed > 0 {
		return 1
	}
	return exitOK
}
