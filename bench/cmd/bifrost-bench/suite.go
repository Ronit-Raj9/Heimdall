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
	register("suite", "lint or lock a benchmark pack: suite lint|lock <id>", runSuite)
}

func runSuite(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench suite lint|lock [-root benchmarks] <suite id>")
		return exitUsage
	}
	sub := args[0]
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
