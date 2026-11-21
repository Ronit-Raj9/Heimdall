package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/lib"
)

// importBenchmarksDir is the config's resolved benchmarks_root, the folder
// suite lint, plan and run read suites from. A missing config means the
// default (empty: <root>/benchmarks), since import works before a config
// exists; a config that exists but cannot be loaded is an error.
func importBenchmarksDir(config string) (string, error) {
	if _, err := os.Stat(config); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		return "", err
	}
	if cfg.BenchmarksRoot == "" {
		return "", nil
	}
	return cfg.Path(cfg.BenchmarksRoot), nil
}

func init() {
	register("import", "convert tasks of a public benchmark into a suite: import list | import <format> -from <path> -suite <id>", runImport)
}

const importUsage = `usage: bifrost-bench import list
       bifrost-bench import <format> -from <path> -suite <id> [-ids a,b] [-limit n] [-set key=value ...] [-force]
           [-config deploy/bench.yaml]`

// setFlags collects repeated -set key=value pairs.
type setFlags map[string]string

func (s setFlags) String() string { return "" }

func (s setFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("want key=value, got %q", v)
	}
	s[k] = val
	return nil
}

func runImport(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, importUsage)
		return exitUsage
	}
	if args[0] == "list" {
		for _, in := range importers.All() {
			fmt.Printf("%-16s %s\n", in.Name, in.Summary)
			fmt.Printf("%-16s benchmarks: %s\n", "", strings.Join(in.Benchmarks, ", "))
			fmt.Printf("%-16s track: %s, grading: %s, license: %s\n", "", in.Track, in.Grading, in.License)
			fmt.Printf("%-16s get it: %s\n", "", in.Source)
			for _, n := range in.Needs {
				fmt.Printf("%-16s needs: %s\n", "", n)
			}
		}
		return exitOK
	}
	im, ok := importers.Get(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown import format %q; run `bifrost-bench import list`\n", args[0])
		return exitUsage
	}
	fs := flag.NewFlagSet("import "+args[0], flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	from := fs.String("from", "", "local path of the upstream benchmark (a clone or a downloaded dataset)")
	suiteID := fs.String("suite", "", "suite id to create or extend")
	ids := fs.String("ids", "", "comma-separated upstream task ids (default: all)")
	limit := fs.Int("limit", 0, "import at most this many tasks")
	force := fs.Bool("force", false, "replace task folders that already exist")
	extra := setFlags{}
	fs.Var(extra, "set", "importer-specific setting, key=value (repeatable)")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 0 || *from == "" || *suiteID == "" {
		fmt.Fprintln(os.Stderr, importUsage)
		return exitUsage
	}
	// The config every suite command reads: deploy/bench.yaml here, else
	// the repository's .heimdall project, so lint and plan find the suite.
	cfgPath := resolveConfigPath(*config)
	root, err := projectRoot(cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	src, err := filepath.Abs(*from)
	if err != nil {
		return fail("%v", err)
	}
	benchmarks, err := importBenchmarksDir(cfgPath)
	if err != nil {
		return fail("%v", err)
	}
	opts := importers.Options{BenchRoot: root, BenchmarksDir: benchmarks, From: src, SuiteID: *suiteID, Limit: *limit, Force: *force, Extra: extra}
	if *ids != "" {
		opts.IDs = strings.Split(*ids, ",")
	}
	res, err := im.Import(context.Background(), opts)
	if err != nil {
		return fail("import %s: %v", args[0], err)
	}
	fmt.Printf("suite %s: %d task(s) written to %s\n", res.Suite, len(res.Tasks), res.SuiteDir)
	for _, s := range res.Skipped {
		fmt.Printf("  skipped %s: %s\n", s.ID, s.Reason)
	}
	for i, n := range res.Notes {
		fmt.Printf("  next %d: %s\n", i+1, n)
	}
	return exitOK
}
