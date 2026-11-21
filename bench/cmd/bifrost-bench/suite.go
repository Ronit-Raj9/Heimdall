package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/ops"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("suite", "create, lint, lock or admit a benchmark pack: suite init|lint|lock|admit <id>", runSuite)
}

const suiteUsage = `usage: bifrost-bench suite init <suite id> -template python-pytest|node-test|go-test|generic-reward|tool-use|mcp|browser|memory|qa|artifact|external-api|computer-use|research [-image <existing image>] [-config deploy/bench.yaml]
       bifrost-bench suite lint|lock [-config deploy/bench.yaml | -root benchmarks] [-pin-local-images] <suite id>
       bifrost-bench suite admit [-config deploy/bench.yaml] [-task t01 ...] [-tasks-file ids.txt] [-parallel N] <suite id>
       bifrost-bench suite health|quarantine|restore|canary|canary-check ... (run one with -h for its flags)`

func runSuite(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	sub := args[0]
	if run, ok := suiteSubcommands[sub]; ok { // Suite health area: health, quarantine, restore, canary, canary-check (suitehealth.go)
		return run(args[1:])
	}
	switch sub {
	case "admit":
		return runAdmit(args[1:])
	case "init":
		return runSuiteInit(args[1:])
	}
	fs := flag.NewFlagSet("suite "+sub, flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	pinLocal := fs.Bool("pin-local-images", false, "lock: record local image ids of locally built images (valid only on this machine); default: tag only, kept as the previous lock did when the flag is not given")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	benchRoot, cfg, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	ctx := context.Background()
	// Jobs area: lint and lock are bench/ops functions the suite.lint and
	// suite.lock jobs run too.
	switch sub {
	case "lint":
		rep, err := ops.SuiteLint(ctx, benchRoot, pos[0], os.Stdout)
		if err != nil {
			return fail("%v", err)
		}
		if rep.Errors > 0 {
			return 1
		}
		return exitOK
	case "lock":
		var pin *bool // Lock fingerprints: pinning is sticky unless the flag says otherwise
		if flagSet(fs, "pin-local-images") {
			pin = pinLocal
		}
		if _, err := ops.SuiteLock(ctx, ops.LockOptions{BenchRoot: benchRoot, Ref: pos[0], Config: cfg, Docker: utils.Docker{}, Pin: pin}, os.Stdout); err != nil {
			return fail("%v", err)
		}
		return exitOK
	default:
		if _, err := loadSuite(ctx, benchRoot, pos[0]); err != nil {
			return fail("load suite %s: %v", pos[0], err)
		}
		fmt.Fprintf(os.Stderr, "unknown suite subcommand %q\n%s\n", sub, suiteUsage)
		return exitUsage
	}
}

// runSuiteInit writes benchmarks/<id>/ from a suite template.
func runSuiteInit(args []string) int {
	fs := flag.NewFlagSet("suite init", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config)")
	tmpl := fs.String("template", "", strings.Join(scaffold.SuiteTemplates(), ", "))
	image := fs.String("image", "", "existing toolchain image to use instead of building base/Dockerfile")
	force := fs.Bool("force", false, "write into an existing suite folder, replacing the template's files")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || *tmpl == "" {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: pos[0], Template: *tmpl, Image: *image})
	if err != nil {
		return fail("suite init: %v", err)
	}
	dir := filepath.Join(benchRoot, pos[0])
	empty, err := isEmptyDir(dir)
	if err != nil {
		return fail("suite init: %v", err)
	}
	if !empty && !*force {
		return fail("%s exists and is not empty; pass -force to write the template's files into it", dir)
	}
	if err := scaffold.WriteFiles(dir, files, *force); err != nil {
		return fail("suite init: %v", err)
	}
	for _, f := range files {
		fmt.Printf("wrote %s\n", filepath.Join(dir, filepath.FromSlash(f.Path)))
	}
	if scaffold.IsScenarioTemplate(*tmpl) {
		sample := scaffold.ScenarioSample(*tmpl)
		fmt.Printf("\nnext: read %s, then\n  bifrost-bench task validate %s/%s\n  bifrost-bench task admit %s/%s\n  bifrost-bench task init -suite %s -id t02\n",
			filepath.Join(dir, "README.md"), pos[0], sample, pos[0], sample, pos[0])
		return exitOK
	}
	fmt.Printf("\nnext: bifrost-bench task init -suite %s -id t01 -repo <git repo> -base <commit> -reference <commit>\n", pos[0])
	return exitOK
}

// loadSuite loads a suite with the first registered loader.
func loadSuite(ctx context.Context, root, ref string) (*schemas.Suite, error) {
	return ops.LoadSuite(ctx, root, ref)
}

// runAdmit runs the admission controls for every task (or one) with the real
// environment and grader, records each result in the suite lock, and writes
// the lock. A task whose controls fail stays a draft and cannot be planned.
func runAdmit(args []string) int {
	fs := flag.NewFlagSet("suite admit", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	var tasks multiFlag
	fs.Var(&tasks, "task", "admit only this task (repeatable)")
	tasksFile := fs.String("tasks-file", "", "file of task ids to admit, one per line (# comments allowed)")
	parallel := fs.Int("parallel", 1, "admit this many tasks at once")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	ids := []string(tasks)
	if *tasksFile != "" {
		more, err := readTaskIDs(*tasksFile)
		if err != nil {
			return fail("%v", err)
		}
		if len(more) == 0 {
			return fail("%s lists no tasks", *tasksFile)
		}
		ids = append(ids, more...)
	}
	return admitSuiteMany(context.Background(), *config, pos[0], ids, *parallel)
}

// multiFlag is a repeatable string flag.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// readTaskIDs reads one task id per line; blank lines and # comments skip.
func readTaskIDs(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}

// admitSuiteMany admits the chosen tasks (every task when ids is empty) with
// parallel workers through ops.SuiteAdmitMany: the suite loads once, only the
// chosen tasks are hashed, suite.lock is written after each task, and a
// refused or failed task is recorded and skipped. It exits 0 when every task
// ran, admitted or not.
func admitSuiteMany(ctx context.Context, config, ref string, ids []string, parallel int) int {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	if _, err := ops.SuiteAdmitMany(ctx, a.admitEnv(config), ref, ops.AdmitManyOptions{Tasks: ids, Parallel: parallel}, os.Stdout); err != nil {
		return fail("%v", err)
	}
	return exitOK
}

// admitSuite admits the tasks of suite ref (only one when only is set) with
// the environment and grader the suite names, looked up by name in the
// registry, builds the suite's base image and the helper image first when
// they are missing, and writes every result to suite.lock. Before any image
// is built or container started it runs the static validation of `task
// validate` on every task it admits and refuses, writing nothing, on any
// error.
func admitSuite(ctx context.Context, config, ref, only string) int {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	rep, err := ops.SuiteAdmit(ctx, a.admitEnv(config), ref, only, os.Stdout) // Jobs area: shared with the suite.admit job (bench/ops)
	if err != nil {
		return fail("%v", err)
	}
	if rep.Failed > 0 {
		return 1
	}
	return exitOK
}

// admitEnv is what ops.SuiteAdmit needs from the app: a judged suite asks
// for a registry with the live gateway, assembled from config as newApp does.
func (a *app) admitEnv(config string) ops.AdmitEnv {
	return ops.AdmitEnv{Config: a.cfg, Docker: a.docker, Logger: a.logger,
		Registry: func(ctx context.Context, gateway bool) (*schemas.Registry, error) {
			if !gateway {
				return a.reg, nil
			}
			ga, err := newApp(ctx, config, appOptions{gateway: true})
			if err != nil {
				return nil, err
			}
			return ga.reg, nil
		}}
}

// printProblems prints lint and validation problems, one per line, as
// `task validate` and `suite admit` show them.
func printProblems(problems []suite.Problem) {
	for _, p := range problems {
		fmt.Printf("%-7s %s: %s\n", p.Severity, p.Path, p.Message)
	}
}

// printAdmission prints one task's admission checks.
func printAdmission(id string, rec *schemas.AdmissionRecord) {
	ops.PrintAdmission(os.Stdout, id, rec)
}

// ensureImages builds the suite's missing images (ops.EnsureImages).
func (a *app) ensureImages(ctx context.Context, s *schemas.Suite) error {
	return ops.EnsureImages(ctx, a.cfg, a.docker, s, os.Stdout)
}

// serviceBuild is one service image ops.EnsureImages looks after.
type serviceBuild struct {
	service, tag string
	root         string // folder the Dockerfile must stay inside
	dockerfile   string // relative to root; empty when nothing builds it
}

// serviceBuilds lists the service images of a suite, suite services first
// (ops.ServiceBuilds).
func serviceBuilds(s *schemas.Suite) []serviceBuild {
	var out []serviceBuild
	for _, b := range ops.ServiceBuilds(s) {
		out = append(out, serviceBuild{service: b.Service, tag: b.Tag, root: b.Root, dockerfile: b.Dockerfile})
	}
	return out
}
