package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

func init() {
	register("suite", "create, lint, lock or admit a benchmark pack: suite init|lint|lock|admit <id>", runSuite)
}

const suiteUsage = `usage: bifrost-bench suite init <suite id> -template python-pytest|node-test|go-test|generic-reward [-image <existing image>] [-config deploy/bench.yaml]
       bifrost-bench suite lint|lock [-config deploy/bench.yaml | -root benchmarks] <suite id>
       bifrost-bench suite admit [-config deploy/bench.yaml] [-task t01] <suite id>`

func runSuite(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	sub := args[0]
	switch sub {
	case "admit":
		return runAdmit(args[1:])
	case "init":
		return runSuiteInit(args[1:])
	}
	fs := flag.NewFlagSet("suite "+sub, flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	pos, err := parseArgs(fs, args[1:])
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	s, err := loadSuite(context.Background(), benchRoot, pos[0])
	if err != nil {
		return fail("load suite %s: %v", pos[0], err)
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
	fmt.Printf("\nnext: bifrost-bench task init -suite %s -id t01 -repo <git repo> -base <commit> -reference <commit>\n", pos[0])
	return exitOK
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
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteUsage)
		return exitUsage
	}
	return admitSuite(context.Background(), *config, pos[0], *only)
}

// admitSuite admits the tasks of suite ref (only one when only is set) with
// the environment and grader the suite names, looked up by name in the
// registry, builds the suite's base image and the helper image first when
// they are missing, and writes every result to suite.lock.
func admitSuite(ctx context.Context, config, ref, only string) int {
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	s, err := loadSuite(ctx, a.cfg.Path(a.cfg.BenchmarksRoot), ref)
	if err != nil {
		return fail("load suite %s: %v", ref, err)
	}
	if only != "" && s.TaskByID(only) == nil {
		return fail("suite %s has no task %q in its tasks list", s.ID, only)
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
	if deps.Env == nil {
		return fail("suite %s: environment %q is not registered", s.ID, s.Environment)
	}
	if deps.Grader == nil {
		return fail("suite %s: grader %q is not registered", s.ID, s.Grader)
	}
	if err := a.ensureImages(ctx, s); err != nil {
		return fail("%v", err)
	}
	lock, err := suite.Lock(s, time.Now().UTC())
	if err != nil {
		return fail("lock: %v", err)
	}
	failed := 0
	for _, t := range s.Tasks {
		if only != "" && t.ID != only {
			continue
		}
		rec, err := suite.Admit(ctx, deps, s, t)
		if err != nil {
			return fail("admit %s: %v", t.ID, err)
		}
		if err := suite.RecordAdmission(lock, t.ID, rec); err != nil {
			return fail("%v", err)
		}
		if !rec.Admitted {
			failed++
		}
		printAdmission(t.ID, rec)
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

// printAdmission prints one task's admission checks.
func printAdmission(id string, rec *schemas.AdmissionRecord) {
	status := "admitted"
	if !rec.Admitted {
		status = "NOT ADMITTED"
	}
	fmt.Printf("%-4s %s\n", id, status)
	for _, c := range rec.Checks {
		mark := "ok  "
		if !c.Passed {
			mark = "FAIL"
		}
		fmt.Printf("     %s %-40s want %-6s got %s\n", mark, c.Name, c.Want, c.Got)
		if !c.Passed && c.Detail != "" {
			fmt.Printf("          %s\n", c.Detail)
		}
	}
}

// ensureImages builds the suite's base image from base_image.dockerfile and
// the helper image from <project>/deploy/helper/Dockerfile, each only when it
// is missing, so a fresh project needs nothing but the binary and Docker. An
// image that exists is never rebuilt.
func (a *app) ensureImages(ctx context.Context, s *schemas.Suite) error {
	suiteDockerfile := ""
	if s.BaseImage.Dockerfile != "" {
		suiteDockerfile = filepath.Join(s.Root, filepath.FromSlash(s.BaseImage.Dockerfile))
	}
	if err := a.ensureImage(ctx, s.BaseImage.Tag, suiteDockerfile); err != nil {
		return fmt.Errorf("suite %s base image: %w", s.ID, err)
	}
	if helper := a.cfg.Images.Helper; helper != "" && helper != s.BaseImage.Tag {
		helperDockerfile := filepath.Join(a.cfg.Root, "deploy", "helper", "Dockerfile")
		if _, err := os.Stat(helperDockerfile); err != nil {
			helperDockerfile = ""
		}
		if err := a.ensureImage(ctx, helper, helperDockerfile); err != nil {
			return fmt.Errorf("helper image (images.helper): %w", err)
		}
	}
	return nil
}

// ensureImage builds tag from dockerfile (in the Dockerfile's folder) when
// docker does not have the image.
func (a *app) ensureImage(ctx context.Context, tag, dockerfile string) error {
	if tag == "" {
		return fmt.Errorf("no image tag")
	}
	res, err := a.docker.Run(ctx, "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return err
	}
	if res.ExitCode == 0 {
		return nil
	}
	if dockerfile == "" {
		return fmt.Errorf("image %s is missing and no Dockerfile builds it; build or pull it first", tag)
	}
	fmt.Printf("building %s from %s\n", tag, dockerfile)
	bin := a.docker.Binary
	if bin == "" {
		bin = "docker"
	}
	cmd := exec.CommandContext(ctx, bin, "build", "-t", tag, "-f", dockerfile, filepath.Dir(dockerfile))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}
