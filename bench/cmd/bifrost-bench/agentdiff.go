package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maximhq/bifrost/bench/agentdiff"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/store"
)

const agentDiffUsage = `usage: bifrost-bench agent diff -a <hash|exp_id:arm> -b <hash|exp_id:arm> [-suite id@version] [-condition clean] [-synthetic] [-json] [-config deploy/bench.yaml] [-runs <dir>]
       bifrost-bench agent diff -agent <agent id> [-suite id@version] ...
What changed between two versions of an agent config, and which tasks flipped, over verified sealed runs of one suite version under one condition.
A version is a config hash (or a prefix of 8 or more characters) or an experiment arm <exp_id>:<arm>. With -agent and no -a/-b, B is the agent's newest config hash on the suite and A the one before it.
Reads the run folder only; no gateway is needed. Exit 64 when the versions cannot be compared (no common task, unknown version).`

// runAgentDiff prints the agent diff of two versions as text or JSON.
func runAgentDiff(args []string, out io.Writer) int {
	fs := flag.NewFlagSet("agent diff", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config; its runs_root is read")
	runs := fs.String("runs", "", "runs root to read instead of the config's (for example testdata/recorded)")
	a := fs.String("a", "", "version A: config hash or <exp_id>:<arm>")
	b := fs.String("b", "", "version B: config hash or <exp_id>:<arm>")
	agent := fs.String("agent", "", "with no -a/-b: compare this agent id's two newest config hashes")
	suiteRef := fs.String("suite", "", "suite id@version (default: the newest suite both versions ran)")
	condition := fs.String("condition", "", "condition: clean (default) or a probe id")
	synthetic := fs.Bool("synthetic", false, "let synthetic runs in (scripted agent, scripted model, smoke experiment)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, agentDiffUsage)
		return exitUsage
	}
	q := agentdiff.Query{A: *a, B: *b, Agent: *agent, Suite: *suiteRef, Condition: *condition, Synthetic: *synthetic}
	if _, err := q.Normalize(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, agentDiffUsage)
		return exitUsage
	}
	ctx := context.Background()
	root, runsRoot := "", *runs
	if runsRoot == "" {
		cfg, err := loadConfig(*config)
		if err != nil {
			return fail("%v", err)
		}
		root, runsRoot = cfg.Root, cfg.Path(cfg.RunsRoot)
	}
	st, err := store.NewFSStore(runsRoot, nil)
	if err != nil {
		return fail("open run store: %v", err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		return fail("index runs: %v", err)
	}
	src, ok := handlers.AgentDiffSource(indexAdapter{ix: ix, root: st.Root()})
	if !ok {
		return fail("the run index cannot verify seals")
	}
	report, err := agentdiff.Diff(ctx, src, agentdiff.RootContents(root), q)
	if errors.Is(err, agentdiff.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return exitUsage
	}
	if err != nil {
		return fail("agent diff: %v", err)
	}
	if *asJSON {
		body, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fail("encode agent diff: %v", err)
		}
		_, err = fmt.Fprintf(out, "%s\n", body)
		if err != nil {
			return fail("write agent diff: %v", err)
		}
		return exitOK
	}
	if _, err := io.WriteString(out, agentDiffText(report)); err != nil {
		return fail("write agent diff: %v", err)
	}
	return exitOK
}

// agentDiffText renders a report for the terminal: the two versions, the
// summary, the config changes by group, the task table, metric deltas, gate
// firings and what was left out. It is pure.
func agentDiffText(r *agentdiff.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "agent diff  %s  condition %s\n", r.Suite, r.Condition)
	for _, v := range []struct {
		side string
		v    agentdiff.Version
	}{{"A", r.A}, {"B", r.B}} {
		where := ""
		if v.v.ExpID != "" {
			where = "  (" + v.v.ExpID + ":" + v.v.Arm + ")"
		}
		fmt.Fprintf(&b, "%s  %s  %s  %s%s  %d verified run(s)\n", v.side, v.v.AgentID, v.v.Label, diffShortHash(v.v.ConfigHash), where, v.v.Runs)
	}
	fmt.Fprintf(&b, "\n%s\n", r.Summary)
	writeConfigText(&b, r.Config)
	writeOutcomeText(&b, r)
	return b.String()
}

// writeConfigText writes the config changes by group.
func writeConfigText(b *strings.Builder, c agentdiff.ConfigDiff) {
	if c.Identical {
		b.WriteString("\nconfig: identical\n")
		return
	}
	fmt.Fprintf(b, "\nconfig: %d change(s)\n", c.Changes)
	marks := map[string]string{agentdiff.KindChanged: "~", agentdiff.KindAdded: "+", agentdiff.KindRemoved: "-"}
	for _, g := range c.Groups {
		fmt.Fprintf(b, "  %s\n", g.ID)
		for _, f := range g.Fields {
			fmt.Fprintf(b, "    %s %s: %s -> %s\n", marks[f.Kind], f.Path, diffValueText(f.Old), diffValueText(f.New))
		}
		for _, f := range g.Files {
			fmt.Fprintf(b, "    %s file %s  %s -> %s\n", marks[f.Kind], f.Path, diffOrNone(diffShortHash(f.OldHash)), diffOrNone(diffShortHash(f.NewHash)))
			if f.DiffNote != "" {
				fmt.Fprintf(b, "      (%s)\n", f.DiffNote)
			}
			for _, line := range strings.Split(strings.TrimSuffix(f.Diff, "\n"), "\n") {
				if line != "" {
					fmt.Fprintf(b, "      %s\n", line)
				}
			}
			if f.Truncated {
				b.WriteString("      (diff cut)\n")
			}
		}
	}
}

// writeOutcomeText writes the task table, metrics, gates and exclusions.
func writeOutcomeText(b *strings.Builder, r *agentdiff.Report) {
	o := r.Outcome
	fmt.Fprintf(b, "\ntasks: %d common\n", o.CommonTasks)
	for _, t := range o.Tasks {
		fmt.Fprintf(b, "  %-17s %-24s A %d/%d  B %d/%d", t.Class, t.Task, t.PassA, t.NA, t.PassB, t.NB)
		if t.Pair != nil {
			if t.Pair.PairID != "" {
				fmt.Fprintf(b, "  pair %s", t.Pair.PairID)
			} else {
				fmt.Fprintf(b, "  runs %s / %s", t.Pair.A.RunID, t.Pair.B.RunID)
			}
		}
		b.WriteString("\n")
	}
	if len(o.OnlyA) > 0 {
		fmt.Fprintf(b, "  only A ran: %s\n", strings.Join(o.OnlyA, ", "))
	}
	if len(o.OnlyB) > 0 {
		fmt.Fprintf(b, "  only B ran: %s\n", strings.Join(o.OnlyB, ", "))
	}
	b.WriteString("\nmetrics (mean over tasks both observed):\n")
	for _, m := range o.Metrics {
		if m.Delta == nil {
			fmt.Fprintf(b, "  %-17s unknown (A %d, B %d run(s) without a value)\n", m.Metric, m.UnknownA, m.UnknownB)
			continue
		}
		fmt.Fprintf(b, "  %-17s A %s  B %s  delta %s  %s\n", m.Metric, diffNumber(*m.A, false), diffNumber(*m.B, false), diffNumber(*m.Delta, true), m.Direction)
	}
	if len(o.Gates) > 0 {
		b.WriteString("\ngates fired:\n")
		for _, g := range o.Gates {
			label := ""
			if g.New {
				label = "  NEW in B"
			}
			fmt.Fprintf(b, "  %-24s A %d  B %d%s", g.Gate, g.FiredA, g.FiredB, label)
			if len(g.Tasks) > 0 {
				fmt.Fprintf(b, "  on %s", strings.Join(g.Tasks, ", "))
			}
			b.WriteString("\n")
		}
	}
	for _, v := range []struct {
		side string
		v    agentdiff.Version
	}{{"A", r.A}, {"B", r.B}} {
		for _, x := range v.v.Excluded {
			fmt.Fprintf(b, "left out of %s: %d run(s), %s\n", v.side, x.Runs, x.Reason)
		}
	}
}

// diffValueText writes a field value compactly; an absent one reads "(unset)".
func diffValueText(v any) string {
	if v == nil {
		return "(unset)"
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// diffShortHash is the first 12 characters of a hash.
func diffShortHash(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// diffNumber writes a metric value: whole numbers from 1000 up without
// exponent, smaller ones to four significant digits. signed adds a + sign.
func diffNumber(v float64, signed bool) string {
	format := "%.4g"
	if v >= 1000 || v <= -1000 {
		format = "%.0f"
	}
	if signed {
		format = strings.Replace(format, "%", "%+", 1)
	}
	return fmt.Sprintf(format, v)
}

// diffOrNone writes "(none)" for an empty string.
func diffOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
