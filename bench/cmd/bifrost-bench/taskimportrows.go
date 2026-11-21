package main

// task import-rows (lane X13): the CLI side of POST /suites/{id}/rows. Rows of
// a CSV or JSON Lines file, or support tickets, become answer tasks of a
// question-and-answer suite, graded by the suite's grader (answer_match or
// judge).

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

func runTaskImportRows(args []string) int {
	fs := flag.NewFlagSet("task import-rows", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteID := fs.String("suite", "", "question-and-answer suite id (a folder under benchmarks/)")
	file := fs.String("file", "", "CSV, JSON Lines or tickets file")
	format := fs.String("format", "", "csv, jsonl or tickets (default from the file extension)")
	match := fs.String("match", "", "answer_match mode of rows that name none: exact (default), contains_all, regex, numeric, choice")
	heldOut := fs.Bool("held-out", false, "mark every imported task held out")
	dryRun := fs.Bool("dry-run", false, "print what would be written and write nothing")
	if _, err := parseArgs(fs, args); err != nil || *suiteID == "" || *file == "" {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench task import-rows -suite <suite> -file <rows.csv|rows.jsonl|tickets.json> [-format csv|jsonl|tickets] [-match exact] [-held-out] [-dry-run]")
		return exitUsage
	}
	root, _, err := benchmarksRoot(fs, "", projectConfig(*config, flagSet(fs, "config")))
	if err != nil {
		return fail("%v", err)
	}
	f := *format
	if f == "" {
		switch filepath.Ext(*file) {
		case ".csv", ".tsv":
			f = scaffold.RowFormatCSV
		case ".jsonl", ".ndjson":
			f = scaffold.RowFormatJSONL
		default:
			f = scaffold.RowFormatTickets
		}
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return fail("%v", err)
	}
	dir := filepath.Join(root, *suiteID)
	suitePath := filepath.Join(dir, suite.SuiteFile)
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		return fail("suite %s: %v", *suiteID, err)
	}
	var s struct {
		Grader string   `yaml:"grader"`
		Tasks  []string `yaml:"tasks"`
		Output struct {
			Kind string `yaml:"kind"`
		} `yaml:"output"`
	}
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return fail("suite.yaml: %v", err)
	}
	if s.Output.Kind != "answer" || (s.Grader != scaffold.RowGraderExact && s.Grader != scaffold.RowGraderJudge) {
		return fail("suite %s is not a question-and-answer suite graded by answer_match or judge (create one with `suite init -template qa`)", *suiteID)
	}
	rows, problems, err := scaffold.ParseRows(f, data)
	if err != nil {
		return fail("%v", err)
	}
	taken := map[string]bool{}
	for _, id := range s.Tasks {
		taken[id] = true
	}
	tasks, more := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: s.Grader, Match: *match, HeldOut: *heldOut,
		Source: filepath.Base(*file), Taken: func(id string) bool {
			if taken[id] {
				return true
			}
			_, err := os.Lstat(filepath.Join(dir, "tasks", id))
			return err == nil
		}})
	for _, p := range append(problems, more...) {
		fmt.Fprintf(os.Stderr, "skip    row %d: %s\n", p.Line, p.Message)
	}
	edited := raw
	for _, t := range tasks {
		fmt.Printf("task    %s  (row %d) %s\n", t.ID, t.Line, t.Title)
		if *dryRun {
			continue
		}
		if err := scaffold.WriteFiles(filepath.Join(dir, "tasks", t.ID), t.Files, false); err != nil {
			return fail("write %s: %v", t.ID, err)
		}
		if edited, _, err = scaffold.AddTaskID(edited, t.ID); err != nil {
			return fail("%v", err)
		}
	}
	if !*dryRun && len(tasks) > 0 {
		if err := os.WriteFile(suitePath, edited, 0o644); err != nil {
			return fail("%v", err)
		}
		fmt.Printf("added %d tasks to %s; next: bifrost-bench suite admit %s\n", len(tasks), *suiteID, *suiteID)
	}
	if len(tasks) == 0 {
		return 1 // no row could become a task
	}
	return exitOK
}
