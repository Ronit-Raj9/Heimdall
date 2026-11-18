package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
)

// runTaskInitCompose writes benchmarks/<suite>/tasks/<id> from a team's own
// docker compose file (X2, docs/multi-container.md): the file's folder is
// copied into the task's stack/ folder, task.yaml names it, the drafts carry
// EDIT ME lines, and every setting the bench refused, changed or ignored is
// printed, as lint will report it.
func runTaskInitCompose(fs *flag.FlagSet, pos []string, config, suiteID, id, file, license string, force bool) int {
	if len(pos) != 0 || suiteID == "" || id == "" {
		fmt.Fprintln(os.Stderr, taskUsage)
		return exitUsage
	}
	for _, f := range []string{"repo", "base", "reference", "kind", "from", "tests"} {
		if flagSet(fs, f) {
			return fail("-from-compose takes the task from the compose file; leave out -%s", f)
		}
	}
	root, _, err := benchmarksRoot(fs, "", config)
	if err != nil {
		return fail("%v", err)
	}
	if !scaffold.ValidID(suiteID) {
		return fail("suite id %q is not valid", suiteID)
	}
	suiteDir := filepath.Join(root, suiteID)
	res, err := compose.NewTask(compose.NewTaskOptions{SuiteDir: suiteDir, ID: id, ComposeFile: file, License: license, Force: force})
	if err != nil {
		return fail("task init: %v", err)
	}
	suitePath := filepath.Join(suiteDir, suite.SuiteFile)
	src, err := os.ReadFile(suitePath)
	if err != nil {
		return fail("task init: %v", err)
	}
	out, added, err := scaffold.AddTaskID(src, id)
	if err != nil {
		return fail("task init: %v", err)
	}
	if added {
		if err := os.WriteFile(suitePath, out, 0o644); err != nil {
			return fail("task init: %v", err)
		}
	}
	printComposeInit(res, suiteID, id, added)
	return exitOK
}

// printComposeInit reports the new task, what the compose mapping found, a
// secret scan of the copy, and the next steps.
func printComposeInit(res *compose.NewTaskResult, suiteID, id string, added bool) {
	fmt.Printf("wrote %s (compose file %s, %d files copied)\n", res.Dir, res.Compose, res.Copied)
	if res.Workspace != "" {
		fmt.Printf("  workspace (the agent's container): %s\n", res.Workspace)
	}
	if len(res.Services) > 0 {
		fmt.Printf("  services beside it, in start order: %s\n", strings.Join(res.Services, ", "))
	}
	if added {
		fmt.Printf("  added %s to the tasks list of suite.yaml\n", id)
	}
	for _, s := range res.Skipped {
		fmt.Printf("  not copied: %s\n", s)
	}
	refused := 0
	for _, level := range []string{compose.LevelRefused, compose.LevelChanged, compose.LevelIgnored} {
		for _, f := range res.Findings {
			if f.Level != level {
				continue
			}
			if level == compose.LevelRefused {
				refused++
			}
			fmt.Printf("%s: %s: %s\n", level, f.Where, f.Message)
		}
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
	if refused > 0 {
		fmt.Printf("  %d. fix the %d refused setting(s) in %s: the task does not load until then\n", step, refused, filepath.Join(res.Dir, filepath.FromSlash(res.Compose)))
		step++
	}
	if res.Workspace == "" {
		fmt.Printf("  %d. mark the service the agent works in with x-heimdall: {workspace: true}\n", step)
		step++
	}
	for _, rel := range res.EditMe {
		fmt.Printf("  %d. edit %s and delete its EDIT ME lines\n", step, filepath.Join(res.Dir, filepath.FromSlash(rel)))
		step++
	}
	fmt.Printf("  %d. bifrost-bench task validate %s/%s\n  %d. bifrost-bench task admit %s/%s\n", step, suiteID, id, step+1, suiteID, id)
}
