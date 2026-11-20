package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/scaffold"
)

func init() {
	register("init", "scaffold a project: init <dir> [-force], or init inside a git repository to detect tests and agents and write .heimdall/", runInit)
}

// runInit writes a new project folder: deploy/, agents/, benchmarks/,
// experiments/, policies/, .gitignore and README.md. A folder that exists and
// is not empty is refused unless -force, which then replaces the scaffold's
// own files and leaves every other file alone.
func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	force := fs.Bool("force", false, "write into a folder that is not empty, replacing the scaffold's files")
	name := fs.String("name", "", "project name for the README (default: the folder name)")
	detect := fs.Bool("detect", false, "inside a git repository: detect tests, agent CLIs and Docker, and write .heimdall/ (the default with no folder argument)")                                                                                   // First workflow (quickstart.go)
	testCmd := fs.String("test-cmd", "", "with -detect: the repository's test command, overriding the detected one; recorded in heimdall.yaml, and it grades only generic-reward tasks (other templates grade with verifier.command in suite.yaml)") // First workflow (quickstart.go)
	image := fs.String("image", "", "with -detect: toolchain image for the suite (default: the helper image for Python, else base/Dockerfile)")                                                                                                      // First workflow (quickstart.go)
	pos, err := parseArgs(fs, args)
	if err == nil && (*detect || len(pos) == 0) { // First workflow: init inside a repository (quickstart.go)
		return runInitRepo(pos, *testCmd, *image, *name, *force)
	}
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench init <dir> [-force] [-name <project name>]\n       bifrost-bench init [-detect] [-test-cmd <cmd>] [-image <image>] [-force]   (inside a git repository: writes .heimdall/)")
		return exitUsage
	}
	dir, err := filepath.Abs(pos[0])
	if err != nil {
		return fail("init %s: %v", pos[0], err)
	}
	empty, err := isEmptyDir(dir)
	if err != nil {
		return fail("init %s: %v", dir, err)
	}
	if !empty && !*force {
		return fail("%s is not empty; pass -force to write the project files into it anyway", dir)
	}
	project := *name
	if project == "" {
		project = filepath.Base(dir)
	}
	files, err := scaffold.Project(scaffold.ProjectOptions{Name: project})
	if err != nil {
		return fail("%v", err)
	}
	if err := scaffold.WriteFiles(dir, files, *force); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("wrote %d files to %s\n\nnext, from %s:\n", len(files), dir, dir)
	for i, step := range projectNextSteps {
		fmt.Printf("  %d. %s\n", i+1, step)
	}
	fmt.Println("\nREADME.md explains each step.")
	return exitOK
}

// projectNextSteps are the six commands README.md walks through.
var projectNextSteps = []string{
	"cp deploy/.env.example deploy/.env    # fill in three random tokens",
	`cp "$(command -v bifrost-bench)" deploy/bifrost-bench && docker build -t bench/bifrost-bench:dev deploy && docker build -t bench/coding-core-base:1 deploy/helper`,
	"docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --wait",
	"bifrost-bench suite init my-suite -template python-pytest",
	"bifrost-bench task init -suite my-suite -id t01 -repo <git repo> -base <commit> -reference <commit>",
	"bifrost-bench task validate my-suite/t01 && bifrost-bench task admit my-suite/t01",
}

// parseArgs parses flags that may come before, between or after the
// positional arguments ("suite init my-suite -template go-test"), which the
// flag package alone stops at, and returns the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// isEmptyDir reports whether dir is missing or holds nothing.
func isEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// benchmarksRoot resolves the benchmarks folder for a command: -root when
// given, else benchmarks_root of the config file. When the config was not
// given explicitly and cannot be read, it falls back to "benchmarks", so
// `suite lint` keeps working from bench/ without a config.
func benchmarksRoot(fs *flag.FlagSet, root, config string) (string, *lib.Config, error) {
	if root != "" {
		return root, nil, nil
	}
	cfg, err := loadConfig(config)
	if err != nil {
		if flagSet(fs, "config") {
			return "", nil, err
		}
		return "benchmarks", nil, nil
	}
	return cfg.Path(cfg.BenchmarksRoot), cfg, nil
}

// flagSet reports whether the flag name was given on the command line.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
