package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
)

func init() {
	register("task", "cut a task from a commit of your repo, check it, admit it: task init|validate|admit", runTask)
}

const taskUsage = `usage: bifrost-bench task init -suite <suite> -id <task> -repo <git repo> -base <commit> -reference <commit>
           [-tests <glob,...>] [-license <spdx|proprietary>] [-capabilities a,b] [-difficulty easy|medium|hard]
           [-kind bugfix|feature|refactor|performance] [-force] [-config deploy/bench.yaml]
       bifrost-bench task init -suite <suite> -id <task> [-kind tool-use|mcp|browser|memory|qa|artifact|external-api|computer-use|research] [-from <task>] [-visual]
           [-license <spdx|proprietary>] [-force] [-config deploy/bench.yaml]
       bifrost-bench task init -suite <suite> -id <task> -from-compose <docker-compose.yaml> [-license <spdx|proprietary>] [-force] [-config deploy/bench.yaml]
       bifrost-bench task validate [-config deploy/bench.yaml] <suite>/<task>
       bifrost-bench task admit [-config deploy/bench.yaml] <suite>/<task>
       bifrost-bench task from-trace -suite <suite> -id <task> -trace <file> [-kind qa|tool-use|mcp|memory] [-redact=false]
       bifrost-bench task mine [-limit 10] [-since <ref|date>] [-paths a,b] [-min-tests 1]   (same as tasks mine)
       bifrost-bench task review <suite>/<task>
       bifrost-bench task import-rows -suite <suite> -file <rows.csv|rows.jsonl|tickets.json> [-format csv|jsonl|tickets] [-match exact] [-held-out] [-dry-run]`

func runTask(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, taskUsage)
		return exitUsage
	}
	switch args[0] {
	case "init":
		return runTaskInit(args[1:])
	case "validate":
		return runTaskValidate(args[1:])
	case "admit":
		return runTaskAdmit(args[1:])
	case "from-trace": // task from-trace: draft a task from a recorded session (taskfromtrace.go)
		return runTaskFromTrace(args[1:])
	case "mine": // First workflow: task mine is tasks mine (tasksmine.go)
		return runTasksMine(args[1:])
	case "review": // First workflow: mark a mined task's instruction reviewed (tasksmine.go)
		return runTaskReview(args[1:])
	case "import-rows": // X13: question-and-answer tasks from CSV, JSONL or tickets (taskimportrows.go)
		return runTaskImportRows(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown task subcommand %q\n%s\n", args[0], taskUsage)
		return exitUsage
	}
}

// runTaskInit writes benchmarks/<suite>/tasks/<id> from two commits of a git
// repository and adds the id to suite.yaml. The repository is only read.
func runTaskInit(args []string) int {
	fs := flag.NewFlagSet("task init", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteID := fs.String("suite", "", "suite id (a folder under benchmarks/)")
	id := fs.String("id", "", "task id")
	repo := fs.String("repo", "", "git repository, or a folder inside one to cut the task from")
	base := fs.String("base", "", "commit before the change: where the agent starts")
	ref := fs.String("reference", "", "commit with the change: the reference solution and the hidden tests")
	tests := fs.String("tests", "", "comma-separated globs of test files (default: the suite template's)")
	license := fs.String("license", "proprietary", "SPDX license id of the repository, or proprietary")
	caps := fs.String("capabilities", "", "comma-separated capabilities the task exercises")
	difficulty := fs.String("difficulty", "", "easy, medium or hard")
	kind := fs.String("kind", "bugfix", "bugfix, feature, refactor or performance; or tool-use, mcp, memory, qa or artifact for a scenario suite")
	from := fs.String("from", "", "scenario suites: task whose shape is copied (default: the suite's first task)")
	force := fs.Bool("force", false, "replace an existing task folder")
	visualOut := fs.Bool("visual", false, "browser, computer-use and artifact tasks: add output.visual and move the suite to grader visual (X1, docs/visual-grading.md)")
	fromCompose := fs.String("from-compose", "", "a team's docker compose file: copy its folder into the task and map it (docs/multi-container.md)") // X2 (taskcompose.go)
	pos, err := parseArgs(fs, args)
	if err == nil && *fromCompose != "" { // X2: a task from a compose file (taskcompose.go)
		return runTaskInitCompose(fs, pos, *config, *suiteID, *id, *fromCompose, *license, *force)
	}
	if err == nil && len(pos) == 0 && *suiteID != "" && *id != "" && (*repo == "" || scaffold.IsScenarioTemplate(*kind)) {
		if *repo != "" || *base != "" || *ref != "" {
			return fail("-kind %s scaffolds from the suite's sample task; leave out -repo, -base and -reference", *kind)
		}
		explicit := ""
		if flagSet(fs, "kind") {
			explicit = *kind
		}
		return runScenarioTaskInit(fs, *config, *suiteID, scaffold.ScenarioTaskOptions{
			ID: *id, Kind: explicit, From: *from, License: flagValue(fs, "license"), Force: *force,
			Visual: *visualOut, // X1 vision grading
		})
	}
	if err != nil || len(pos) != 0 || *suiteID == "" || *id == "" || *repo == "" || *base == "" || *ref == "" {
		fmt.Fprintln(os.Stderr, taskUsage)
		return exitUsage
	}
	root, cfg, err := benchmarksRoot(fs, "", *config)
	if err != nil {
		return fail("%v", err)
	}
	if !scaffold.ValidID(*suiteID) {
		return fail("suite id %q is not valid", *suiteID)
	}
	opts := scaffold.TaskInitOptions{
		SuiteDir: filepath.Join(root, *suiteID), ID: *id, Repo: *repo, Base: *base, Reference: *ref,
		Tests: splitList(*tests), License: *license, Capabilities: splitList(*caps), Difficulty: *difficulty,
		Kind: *kind, Force: *force,
	}
	if cfg != nil {
		opts.ExcludeDir = cfg.Root
	}
	res, err := scaffold.InitTask(context.Background(), opts)
	if err != nil {
		return fail("task init: %v", err)
	}
	printTaskInit(res, *suiteID, *id)
	return exitOK
}

// scenarioTaskInitError is the command line's text for a failed scenario
// task init: a task type mismatch gets back its flag hint (wave 4, N78).
func scenarioTaskInitError(err error) string {
	var km *scaffold.KindMismatchError
	if errors.As(err, &km) {
		return fmt.Sprintf("task init: %s was written from template %s, not %s; pass -kind %s or leave -kind out", km.Suite, km.Detected, km.Wanted, km.Detected)
	}
	return "task init: " + err.Error()
}

// runScenarioTaskInit writes a new task in a tool-use, mcp, memory or qa
// suite from the shape of an existing task (scaffold.InitScenarioTask).
func runScenarioTaskInit(fs *flag.FlagSet, config, suiteID string, opts scaffold.ScenarioTaskOptions) int {
	root, _, err := benchmarksRoot(fs, "", config)
	if err != nil {
		return fail("%v", err)
	}
	if !scaffold.ValidID(suiteID) {
		return fail("suite id %q is not valid", suiteID)
	}
	opts.SuiteDir = filepath.Join(root, suiteID)
	res, err := scaffold.InitScenarioTask(opts)
	if err != nil {
		return fail("%s", scenarioTaskInitError(err))
	}
	fmt.Printf("wrote %s (template %s, shape of %s)\n", res.Dir, res.Template, res.From)
	if res.SuiteEdited {
		fmt.Printf("  added %s to the tasks list of suite.yaml\n", opts.ID)
	}
	for _, w := range res.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	findings, err := suite.ScanSecrets(res.Dir)
	if err != nil {
		fmt.Printf("warning: %v\n", err)
	}
	for _, f := range findings {
		fmt.Printf("warning: %s; remove it before admitting\n", f)
	}
	fmt.Println("\nnext:")
	step := 1
	for _, rel := range res.EditMe {
		fmt.Printf("  %d. edit %s and delete its EDIT ME line\n", step, filepath.Join(res.Dir, filepath.FromSlash(rel)))
		step++
	}
	fmt.Printf("  %d. review the rest of %s (seed data, session files, controls)\n", step, res.Dir)
	fmt.Printf("  %d. bifrost-bench task validate %s/%s\n  %d. bifrost-bench task admit %s/%s\n", step+1, suiteID, opts.ID, step+2, suiteID, opts.ID)
	return exitOK
}

// flagValue is the value of flag name when it was given, else "".
func flagValue(fs *flag.FlagSet, name string) string {
	if !flagSet(fs, name) {
		return ""
	}
	return fs.Lookup(name).Value.String()
}

// printTaskInit reports what task init wrote, its warnings and a secret scan
// of the new folder, then the next steps.
func printTaskInit(res *scaffold.TaskInitResult, suiteID, id string) {
	fmt.Printf("wrote %s (template %s)\n", res.Dir, orUnknown(res.Template))
	fmt.Printf("  hidden tests (verifier/tests): %s\n", strings.Join(res.Hidden, ", "))
	fmt.Printf("  visible tests listed under protected: %d\n", len(res.Protected))
	fmt.Printf("  required test ids: %d\n", len(res.Required))
	if res.SuiteEdited {
		fmt.Printf("  added %s to the tasks list of suite.yaml\n", id)
	}
	for _, w := range res.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	findings, err := suite.ScanSecrets(res.Dir)
	if err != nil {
		fmt.Printf("warning: %v\n", err)
	}
	for _, f := range findings {
		fmt.Printf("warning: %s; remove it before admitting\n", f)
	}
	fmt.Printf("\nnext:\n  1. rewrite %s as the request a teammate would send, and delete the EDIT ME line\n",
		filepath.Join(res.Dir, "instruction.md"))
	fmt.Printf("  2. review %s: these tests come from the reference commit\n", filepath.Join(res.Dir, "verifier", "tests"))
	fmt.Printf("  3. bifrost-bench task validate %s/%s\n  4. bifrost-bench task admit %s/%s\n", suiteID, id, suiteID, id)
}

// runTaskValidate runs the checks that need no Docker on one task.
func runTaskValidate(args []string) int {
	fs := flag.NewFlagSet("task validate", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, taskUsage)
		return exitUsage
	}
	suiteID, taskID, ok := splitTaskRef(pos[0])
	if !ok {
		return fail("want <suite>/<task>, got %q", pos[0])
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	s, t, err := suite.LoadTask(benchRoot, suiteID, taskID)
	if err != nil {
		return fail("load %s: %v", pos[0], err)
	}
	problems := suite.ValidateTask(context.Background(), s, t)
	printProblems(problems)
	errs := suite.Errors(problems)
	fmt.Printf("%s/%s: %s\n", s.ID, t.ID, validateSummary(len(problems), errs))
	if errs > 0 {
		return 1
	}
	return exitOK
}

// validateSummary is the last line of `task validate`: "2 errors, 1
// warning", or "no problems".
func validateSummary(problems, errs int) string {
	if problems == 0 {
		return "no problems"
	}
	return plural(errs, "error") + ", " + plural(problems-errs, "warning")
}

// runTaskAdmit admits one task and records it in suite.lock, as
// `suite admit -task <task> <suite>` does.
func runTaskAdmit(args []string) int {
	fs := flag.NewFlagSet("task admit", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, taskUsage)
		return exitUsage
	}
	suiteID, taskID, ok := splitTaskRef(pos[0])
	if !ok {
		return fail("want <suite>/<task>, got %q", pos[0])
	}
	return admitSuite(context.Background(), *config, suiteID, taskID)
}

// splitTaskRef splits "<suite>/<task>".
func splitTaskRef(ref string) (string, string, bool) {
	s, t, ok := strings.Cut(strings.Trim(ref, "/"), "/")
	if !ok || s == "" || t == "" || strings.Contains(t, "/") {
		return "", "", false
	}
	return s, t, true
}

// splitList splits a comma-separated flag value, dropping empty items.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
