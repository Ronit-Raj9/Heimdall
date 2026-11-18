package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/store"
)

// Eval integrity area (S2): evaluation awareness (docs/eval-awareness.md).

const awarenessUsage = `usage: bifrost-bench awareness [-config deploy/bench.yaml] -exp <id> [-json]
Scans the agents' own words in every counted sealed run of an experiment for
remarks that they are being tested: the output side of the gateway rows, the
message lines of the agent stream and the final message. It is a heuristic
phrase match that counts words, not intent; report only, never a gate. Reads
the run folder only; no gateway is needed.`

// maxAwarenessNotScannedShown caps the not-scanned runs the text form lists.
const maxAwarenessNotScannedShown = 10

func init() {
	register("awareness", "heuristic eval awareness phrase scan of an experiment's runs", runAwareness)
}

func runAwareness(args []string) int {
	fs := flag.NewFlagSet("awareness", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	exp := fs.String("exp", "", "experiment id")
	asJSON := fs.Bool("json", false, "print the report as JSON (the API's shape)")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, awarenessUsage)
		return exitUsage
	}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	ix := store.NewIndex(a.store)
	if err := ix.Rebuild(ctx); err != nil {
		return fail("index runs: %v", err)
	}
	return printAwareness(ctx, os.Stdout, indexAdapter{ix: ix, root: a.store.Root()}, *exp, *asJSON)
}

// printAwareness builds the report and prints it as JSON or as text.
func printAwareness(ctx context.Context, w io.Writer, index handlers.ReadIndex, expID string, asJSON bool) int {
	rep, err := handlers.BuildAwareness(ctx, index, expID)
	if err != nil {
		return fail("awareness: %v", err)
	}
	if asJSON {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return fail("encode awareness report: %v", err)
		}
		if _, err := fmt.Fprintln(w, string(b)); err != nil {
			return fail("write awareness report: %v", err)
		}
		return exitOK
	}
	if err := writeAwarenessText(w, rep); err != nil {
		return fail("write awareness report: %v", err)
	}
	return exitOK
}

// writeAwarenessText prints the report for a terminal.
func writeAwarenessText(w io.Writer, rep *handlers.AwarenessReport) error {
	fmt.Fprintf(w, "experiment %s  realism %s  scanner %s\n", rep.ExpID, rep.Realism, rep.ScannerVersion)
	fmt.Fprintf(w, "HEURISTIC: %s\n\n", rep.Label)
	arms := make([]string, 0, len(rep.PerArm))
	for arm := range rep.PerArm {
		arms = append(arms, arm)
	}
	sort.Strings(arms)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ARM\tRUNS\tSCANNED\tWITH SIGNALS\tSIGNALS")
	for _, arm := range arms {
		a := rep.PerArm[arm]
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", arm, a.Runs, a.RunsScanned, a.RunsWithSignals, a.Signals)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if len(rep.Examples) > 0 {
		fmt.Fprintln(w, "\nexamples:")
		for _, ex := range rep.Examples {
			fmt.Fprintf(w, "  %s arm %s task %s %s [%s] %q: %s\n", ex.RunID, ex.Arm, ex.Task, ex.Condition, ex.Source, ex.Phrase, ex.Excerpt)
		}
	}
	if n := len(rep.NotScanned); n > 0 {
		fmt.Fprintf(w, "\n%d run(s) had no agent text to scan:\n", n)
		for i, ns := range rep.NotScanned {
			if i == maxAwarenessNotScannedShown {
				fmt.Fprintf(w, "  ... and %d more (-json lists all)\n", n-i)
				break
			}
			fmt.Fprintf(w, "  %s: %s\n", ns.RunID, ns.Reason)
		}
	}
	_, err := fmt.Fprintln(w, "\nNo signal is not evidence of no awareness: a model can recognise a test without saying so.")
	return err
}
