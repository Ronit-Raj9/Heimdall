package main

// X4: `codescan diff` and `codescan rules` (docs/code-scan.md).

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/schemas"
)

func init() {
	register("codescan", "security findings on the lines an agent added: diff <run_id> | diff -file <diff> | rules", runCodeScanCmd)
}

// Exit code of `codescan diff -fail-at` when a counted finding reaches the
// severity.
const exitCodeScanFindings = 1

func runCodeScanCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench codescan diff <run_id> | diff -file <workspace.diff> | rules [-json]")
		return exitUsage
	}
	switch args[0] {
	case "rules":
		return runCodeScanRules(args[1:])
	case "diff":
		return runCodeScanDiff(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown codescan command %q: want diff or rules\n", args[0])
		return exitUsage
	}
}

// runCodeScanRules prints the builtin rules.
func runCodeScanRules(args []string) int {
	fs := flag.NewFlagSet("codescan rules", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench codescan rules [-json]")
		return exitUsage
	}
	rules := codescan.Describe()
	if *asJSON {
		return codeScanJSON(map[string]any{"rules_version": codescan.RulesVersion, "rules": rules})
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "RULE\tCWE\tSEVERITY\tLANGUAGES\tTITLE\n")
	for _, r := range rules {
		title := r.Title
		if r.Removed {
			title += " (removed lines)"
		}
		if r.SkipTests {
			title += " (not in tests)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.CWE, r.Severity, strings.Join(r.Languages, ","), title)
	}
	tw.Flush()
	fmt.Printf("\n%d rules, version %s; `codescan rules -json` gives each rule's summary\n", len(rules), codescan.RulesVersion)
	return exitOK
}

// runCodeScanDiff scans one sealed run, read-only, or any diff file. A run's
// seal is verified before anything is read; its experiment's frozen
// codescan setting decides what counts and what would veto.
func runCodeScanDiff(args []string) int {
	fs := flag.NewFlagSet("codescan diff", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id (default: found from the run id)")
	file := fs.String("file", "", "scan this unified diff file instead of a sealed run")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	failAt := fs.String("fail-at", "", "exit 1 when a counted finding is at or above this severity (low, medium, high, critical)")
	runID := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		runID, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (runID == "") == (*file == "") {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench codescan diff <run_id> [-exp <exp_id>] [-config deploy/bench.yaml] [-json] [-fail-at high]\n       bifrost-bench codescan diff -file <workspace.diff> [-json] [-fail-at high]")
		return exitUsage
	}
	if *failAt != "" && schemas.CodeScanSeverityRank(*failAt) == 0 {
		fmt.Fprintf(os.Stderr, "-fail-at %q is not one of %s\n", *failAt, strings.Join(schemas.CodeScanSeverities, ", "))
		return exitUsage
	}
	var rep *schemas.CodeScanReport
	where := ""
	if *file != "" {
		scan := codescan.ScanFile(*file)
		rep = codescan.BuildReport("", scan, false, nil)
		where = *file
	} else {
		var err error
		if rep, where, err = sealedReport(*config, *exp, runID); err != nil {
			return fail("codescan %s: %v", runID, err)
		}
	}
	code := exitOK
	if *failAt != "" {
		if len(codescan.Vetoing(rep.Scan, &schemas.CodeScanPolicy{VetoAt: *failAt, Ignore: rep.Ignored})) > 0 {
			code = exitCodeScanFindings
		}
	}
	if *asJSON {
		if c := codeScanJSON(rep); c != exitOK {
			return c
		}
		return code
	}
	printCodeScanReport(rep, where)
	return code
}

// sealedReport loads one sealed run's report after checking its seal.
func sealedReport(config, expID, runID string) (*schemas.CodeScanReport, string, error) {
	ctx := context.Background()
	a, err := newApp(ctx, config, appOptions{})
	if err != nil {
		return nil, "", err
	}
	if expID == "" {
		if expID, err = a.store.RunExp(runID); err != nil {
			return nil, "", err
		}
	}
	if err := a.store.Verify(ctx, expID, runID); err != nil {
		return nil, "", fmt.Errorf("seal check failed, nothing read: %w", err)
	}
	dirs, err := filepath.Glob(filepath.Join(a.store.Root(), expID, "slots", "*", "runs", runID))
	if err != nil || len(dirs) != 1 {
		return nil, "", fmt.Errorf("run folder of %s in %s: %d found", runID, expID, len(dirs))
	}
	ev, err := codescan.LoadRun(dirs[0])
	if err != nil {
		return nil, "", err
	}
	var policy *schemas.CodeScanPolicy
	if m, err := a.store.LoadManifest(ctx, expID); err == nil {
		policy = m.Experiment.CodeScan
	}
	scan, sealed := codescan.ForRun(ev)
	return codescan.BuildReport(runID, scan, sealed, policy), dirs[0], nil
}

// printCodeScanReport prints a report as a table.
func printCodeScanReport(rep *schemas.CodeScanReport, where string) {
	s := rep.Scan
	source := "computed now from the sealed diff"
	switch {
	case rep.RunID == "":
		source = "computed from the file"
	case rep.Sealed:
		source = "the run's sealed code scan evidence"
	}
	name := where
	if rep.RunID != "" {
		name = "run " + rep.RunID
	}
	fmt.Printf("%s: rules %s, %s\n", name, s.RulesVersion, source)
	if !s.Scanned {
		fmt.Printf("not scanned: %s\n", s.Reason)
		return
	}
	fmt.Printf("%d findings on %d added lines in %d files", len(s.Findings), s.AddedLines, s.Files)
	if rep.Counted != len(s.Findings) {
		fmt.Printf(" (%d counted, the rest ignored by the experiment)", rep.Counted)
	}
	fmt.Println()
	if len(s.Findings) > 0 {
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(tw, "SEVERITY\tRULE\tCWE\tWHERE\tSOURCE\tSNIPPET\n")
		for _, f := range s.Findings {
			at := fmt.Sprintf("%s:%d", f.File, f.Line)
			if f.Removed {
				at += " (removed)"
			}
			if f.InTest {
				at += " (test)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", f.Severity, f.Rule, f.CWE, at, f.Source, f.Snippet)
		}
		tw.Flush()
	}
	for _, x := range s.External {
		status := fmt.Sprintf("%d results, %d on added lines, exit %d", x.Results, x.OnAdded, x.ExitCode)
		if x.Error != "" {
			status = "failed: " + x.Error
		}
		fmt.Printf("external %s (%s): %s\n", x.Scanner, x.Image, status)
	}
	for _, sk := range s.Skipped {
		fmt.Printf("skipped %s: %s\n", sk.File, sk.Reason)
	}
	if s.Reason != "" {
		fmt.Println(s.Reason)
	}
	if rep.VetoAt != "" {
		verdict := "does not veto"
		if rep.Vetoed {
			verdict = "vetoes the run"
		}
		fmt.Printf("experiment veto level %s: %s\n", rep.VetoAt, verdict)
	}
}

// codeScanJSON writes v as indented JSON.
func codeScanJSON(v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fail("encode: %v", err)
	}
	fmt.Println(string(b))
	return exitOK
}
