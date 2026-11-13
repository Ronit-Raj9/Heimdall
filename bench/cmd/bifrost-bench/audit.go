package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/maximhq/bifrost/bench/verdict"
)

func init() {
	register("audit", "verdict for a published per-task results table (task,a_pass,a_n,b_pass,b_n)", runAudit)
}

func runAudit(args []string) int {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	alpha := fs.Float64("alpha", 0.05, "significance level")
	asJSON := fs.Bool("json", false, "print the verdict as JSON")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: bifrost-bench audit [-alpha 0.05] [-json] table.csv")
		return exitUsage
	}
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		return fail("open %s: %v", fs.Arg(0), err)
	}
	defer f.Close()
	rows, err := verdict.ParseAuditCSV(f)
	if err != nil {
		return fail("parse %s: %v", fs.Arg(0), err)
	}
	v, err := verdict.Audit(rows, *alpha)
	if err != nil {
		return fail("audit: %v", err)
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return fail("encode: %v", err)
		}
		return exitOK
	}
	aPass, aN, bPass, bN := 0, 0, 0, 0
	for _, r := range rows {
		aPass, aN, bPass, bN = aPass+r.APass, aN+r.AN, bPass+r.BPass, bN+r.BN
	}
	fmt.Printf("naive scoreboard: A %d/%d, B %d/%d\n", aPass, aN, bPass, bN)
	fmt.Printf("verdict: %s (action: %s)\n", v.Decision, v.Action)
	if e := v.Effect; e != nil {
		p := "n/a"
		if e.PValue != nil {
			p = fmt.Sprintf("%.4g", *e.PValue)
		}
		fmt.Printf("release effect: %+.1f pp over %d tasks; %d discordant (%d favour B, %d favour A); exact p = %s\n",
			100*e.DBar, e.NTasks, e.NNonzero, e.NPositive, e.NNegative, p)
	}
	fmt.Printf("minimal detectable effect: %.1f pp (sd %.2f%s)\n", v.MDE, v.SD, map[bool]string{true: ", assumed", false: ""}[v.MDEAssumedSD])
	return exitOK
}
