package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/traceimport"
)

const taskFromTraceUsage = `usage: bifrost-bench task from-trace -suite <suite> -id <task> -trace <file> [-kind qa|tool-use|mcp|memory]
           [-session <id>] [-format otlp|langfuse|chat] [-redact=true|false] [-replay=true|false] [-license <spdx|proprietary>]
           [-force] [-max-bytes <n>] [-config deploy/bench.yaml] [-root <benchmarks folder>]
       bifrost-bench task from-trace -list <file> [-format otlp|langfuse|chat] [-redact=true|false]`

// runTaskFromTrace drafts a task from one recorded session (`task
// from-trace`), or lists the sessions of a trace file (-list). The trace is
// a local file; nothing is fetched. Redaction is on unless -redact=false, and
// it runs before anything is written or printed. The draft holds EDIT ME
// markers, so `task validate` refuses it until a person has confirmed what
// the right answer or final state is.
func runTaskFromTrace(args []string) int {
	fs := flag.NewFlagSet("task from-trace", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config)")
	suiteID := fs.String("suite", "", "suite id (a folder under benchmarks/) the draft is added to")
	id := fs.String("id", "", "new task id")
	tracePath := fs.String("trace", "", "trace file: OTLP/JSON, a Langfuse export, or a chat transcript")
	list := fs.String("list", "", "print the sessions of this trace file and exit")
	session := fs.String("session", "", "session (trace id) to draft from, when the file holds several")
	format := fs.String("format", "", "otlp, langfuse or chat (default: detected)")
	kind := fs.String("kind", "", "qa, tool-use, mcp or memory (default: the suite's template)")
	redact := fs.Bool("redact", true, "scrub emails, phone numbers, long digit runs, tokens, API keys and auth headers before writing")
	license := fs.String("license", "", "SPDX license id of the task, or proprietary (default: the copied task's)")
	force := fs.Bool("force", false, "replace an existing task folder")
	replay := fs.Bool("replay", true, "tool-use and mcp: run a replay server of the observed calls in place of the copied tool service")
	maxBytes := fs.Int64("max-bytes", traceimport.DefaultMaxBytes, "largest trace file read")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 0 {
		fmt.Fprintln(os.Stderr, taskFromTraceUsage)
		return exitUsage
	}
	if *list != "" {
		return listTraceSessions(*list, *format, *maxBytes, *redact)
	}
	if *suiteID == "" || *id == "" || *tracePath == "" {
		fmt.Fprintln(os.Stderr, taskFromTraceUsage)
		return exitUsage
	}
	if !scaffold.ValidID(*suiteID) {
		return fail("suite id %q is not valid", *suiteID)
	}
	data, err := traceimport.ReadFile(*tracePath, *maxBytes)
	if err != nil {
		return fail("%v", err)
	}
	tr, err := traceimport.Parse(data, *format)
	if err != nil {
		return fail("%v", err)
	}
	s, err := tr.Pick(*session)
	if err != nil {
		return fail("%v", err)
	}
	sum := sha256.Sum256(data)
	src := traceimport.Source{File: filepath.Base(*tracePath), SHA256: hex.EncodeToString(sum[:]), Format: tr.Format}
	if *redact {
		r := traceimport.NewRedactor()
		r.Session(s)
		src.File = r.Text(src.File)
		src.Redacted = r.Report()
	}
	benchRoot, _, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	res, err := traceimport.WriteDraft(traceimport.DraftOptions{
		SuiteDir: filepath.Join(benchRoot, *suiteID), ID: *id, Kind: *kind, License: *license, Force: *force, Session: s, Source: src, Replay: *replay,
	})
	if err != nil {
		return fail("task from-trace: %v", err)
	}
	printDraft(res, s, src, tr.Warnings, *suiteID, *id)
	return exitOK
}

// printDraft reports what the draft holds, what was scrubbed (by count,
// never by value), a secret scan of the new folder and the next steps.
func printDraft(res *traceimport.DraftResult, s *traceimport.Session, src traceimport.Source, warnings []string, suiteID, id string) {
	fmt.Printf("wrote draft %s (template %s, shape of %s)\n", res.Dir, res.Template, res.From)
	fmt.Printf("  session %s: %d turns, %d tool calls, outcome %s\n", s.ID, len(s.Turns), len(s.ToolCalls), s.Summary())
	if res.SuiteEdited {
		fmt.Printf("  added %s to the tasks list of suite.yaml\n", id)
	}
	if res.Replayed {
		fmt.Printf("  tool service %s replays the %d observed calls (replay/, %s); its calls.json is the state the checks read\n",
			traceimport.ReplayServiceID, len(s.ToolCalls), traceimport.ObservedToolsFile)
	}
	if src.Redacted == nil {
		fmt.Println("warning: redaction is off; the draft holds the trace's text as recorded")
	} else {
		fmt.Printf("  redacted: %s\n", src.Redacted)
	}
	for _, w := range append(warnings, res.Warnings...) {
		fmt.Printf("warning: %s\n", w)
	}
	findings, err := suite.ScanSecrets(res.Dir)
	if err != nil {
		fmt.Printf("warning: %v\n", err)
	}
	for _, f := range findings {
		fmt.Printf("warning: %s; remove it before admitting\n", f)
	}
	fmt.Println("\nThe draft shows what happened in the trace, not what was right. next:")
	step := 1
	for _, rel := range res.EditMe {
		fmt.Printf("  %d. edit %s and delete its EDIT ME lines\n", step, filepath.Join(res.Dir, filepath.FromSlash(rel)))
		step++
	}
	fmt.Printf("  %d. review the rest of %s (%s has the whole session)\n", step, res.Dir, traceimport.TranscriptFile)
	fmt.Printf("  %d. bifrost-bench task validate %s/%s\n  %d. bifrost-bench task admit %s/%s\n", step+1, suiteID, id, step+2, suiteID, id)
}

// listTraceSessions prints the sessions of a trace file, one per line.
func listTraceSessions(path, format string, maxBytes int64, redact bool) int {
	data, err := traceimport.ReadFile(path, maxBytes)
	if err != nil {
		return fail("%v", err)
	}
	tr, err := traceimport.Parse(data, format)
	if err != nil {
		return fail("%v", err)
	}
	r := traceimport.NewRedactor()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "SESSION\tTURNS\tTOOLS\tOUTCOME\tFIRST REQUEST\n")
	for _, s := range tr.Sessions {
		if redact {
			r.Session(s)
		}
		req := strings.Join(strings.Fields(s.FirstRequest()), " ")
		if len(req) > 50 {
			req = strings.ToValidUTF8(req[:50], "") + "..."
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\n", s.ID, len(s.Turns), len(s.ToolCalls), s.Summary(), req)
	}
	if err := tw.Flush(); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("%s format, %d sessions\n", tr.Format, len(tr.Sessions))
	for _, w := range tr.Warnings {
		fmt.Printf("warning: %s\n", w)
	}
	return exitOK
}
