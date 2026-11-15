package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

const clustersUsage = `usage: bifrost-bench clusters [-config deploy/bench.yaml] [-suite id[@version]] [-agent config-id|hash-prefix] [-exp id[,id]] [-since 2026-11-01|7d|24h] [-min n] [-synthetic exclude|include|only] [-json] [signature]
Groups the failed runs of every verified sealed run across experiments by cause (docs/failure-clusters.md). With a signature, prints that cluster and its runs. Reads the run folder only; no gateway is needed.`

// clusterEvidenceMax caps how much of one evidence file a cluster reads.
const clusterEvidenceMax = 64 << 20

// clusterEvidenceFiles are the run files clusters.Source.RunEvidence serves.
// The agent's stderr is read here, never served by the API as a file: a
// cluster shows only its normalized first error line.
var clusterEvidenceFiles = map[string]bool{
	clusters.FileEvents: true,
	clusters.FileLedger: true,
	clusters.FileTools:  true,
	clusters.FileStderr: true,
}

var _ clusters.Source = indexAdapter{}

func init() {
	register("clusters", "group failed runs across experiments by cause", runClusters)
}

// expList collects repeated -exp flags.
type expList []string

func (e *expList) String() string { return strings.Join(*e, ",") }

func (e *expList) Set(v string) error {
	*e = append(*e, v)
	return nil
}

func runClusters(args []string) int {
	fs := flag.NewFlagSet("clusters", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteFlag := fs.String("suite", "", "suite id or id@version")
	agent := fs.String("agent", "", "agent config id, or a config hash prefix of 6 or more characters")
	var exps expList
	fs.Var(&exps, "exp", "experiment id; repeat or comma-separate for several")
	since := fs.String("since", "", "runs started at or after: RFC3339 time, date (2006-01-02) or age (24h, 7d)")
	minCount := fs.Int("min", 1, "smallest cluster listed")
	synthetic := fs.String("synthetic", clusters.SyntheticExclude, "exclude: live runs only; include: all; only: scripted runs only")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, clustersUsage)
		return exitUsage
	}
	q := clusters.Query{Suite: *suiteFlag, Agent: *agent, Experiments: exps, Since: *since, Min: *minCount, Synthetic: *synthetic}
	ctx := context.Background()
	a, err := newApp(ctx, *config, appOptions{})
	if err != nil {
		return fail("%v", err)
	}
	ix := store.NewIndex(a.store)
	if err := ix.Rebuild(ctx); err != nil {
		return fail("index runs: %v", err)
	}
	return printClusters(ctx, os.Stdout, indexAdapter{ix: ix, root: a.store.Root()}, q, fs.Arg(0), *asJSON)
}

// printClusters builds the report and prints it, or one cluster when
// signature is set, as text or JSON. It returns the exit code.
func printClusters(ctx context.Context, w io.Writer, src clusters.Source, q clusters.Query, signature string, asJSON bool) int {
	rep, err := clusters.Build(ctx, src, q)
	if errors.Is(err, clusters.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, clustersUsage)
		return exitUsage
	}
	if err != nil {
		return fail("clusters: %v", err)
	}
	var payload any = rep
	if signature != "" {
		d, err := clusters.Find(rep, signature)
		if err != nil {
			return fail("%v", err)
		}
		payload = d
	}
	if asJSON {
		body, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return fail("encode clusters: %v", err)
		}
		if _, err := fmt.Fprintf(w, "%s\n", body); err != nil {
			return fail("write clusters: %v", err)
		}
		return exitOK
	}
	if d, ok := payload.(*clusters.Detail); ok {
		err = writeClusterDetail(w, d)
	} else {
		err = writeClusterTable(w, rep)
	}
	if err != nil {
		return fail("write clusters: %v", err)
	}
	return exitOK
}

// writeClusterTable prints the clusters as a table, largest first.
func writeClusterTable(w io.Writer, rep *clusters.Report) error {
	fmt.Fprintf(w, "%d failures in %d verified runs across %d experiment(s)", rep.Failures, rep.Runs, len(rep.Experiments))
	for _, x := range rep.Excluded {
		fmt.Fprintf(w, "; %d excluded (%s)", x.Runs, x.Reason)
	}
	fmt.Fprintln(w)
	if len(rep.Clusters) == 0 {
		_, err := fmt.Fprintln(w, "no clusters")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "COUNT\tSHARE\tSCOPE\tSIGNATURE\tTITLE\tTASKS\tAGENTS")
	for _, c := range rep.Clusters {
		fmt.Fprintf(tw, "%d\t%.0f%%\t%s\t%s\t%s\t%s\t%s\n", c.Count, c.Share*100, c.Scope, c.Signature, c.Title,
			strings.Join(c.Tasks, ","), strings.Join(c.Agents, ","))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if rep.Hidden > 0 {
		fmt.Fprintf(w, "%d smaller clusters hidden by -min %d\n", rep.Hidden, rep.Filters.Min)
	}
	return nil
}

// writeClusterDetail prints one cluster with its layers and runs.
func writeClusterDetail(w io.Writer, d *clusters.Detail) error {
	c := d.Cluster
	fmt.Fprintf(w, "%s  %s\n%d of %d failures, scope %s, first %s, last %s\n", c.Signature, c.Title, c.Count, d.Failures, c.Scope,
		c.FirstSeen.Format("2006-01-02T15:04Z"), c.LastSeen.Format("2006-01-02T15:04Z"))
	for _, l := range c.Layers {
		fmt.Fprintf(w, "  %s: %s\n", l.Kind, l.Label)
		for _, it := range l.Items {
			fmt.Fprintf(w, "    %s\n", it)
		}
	}
	if c.AfterConfigChange != nil {
		fmt.Fprintf(w, "appeared after %s changed config %s to %s\n", c.AfterConfigChange.Agent, short(c.AfterConfigChange.FromHash), short(c.AfterConfigChange.ToHash))
	}
	for _, r := range c.Related {
		fmt.Fprintf(w, "related %s (%d runs): %s; %s\n", r.Signature, r.Count, r.Title, r.Reason)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN\tEXPERIMENT\tTASK\tARM\tCONDITION\tAGENT\tOUTCOME\tSTARTED")
	for _, r := range d.Runs {
		fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\t%s\t%s\t%s\t%s\n", r.RunID, r.ExpID, r.Suite, r.Task, r.Arm, r.Condition, r.Agent, r.Outcome,
			r.At.UTC().Format("2006-01-02T15:04Z"))
	}
	return tw.Flush()
}

// VerifiedRuns implements clusters.Source with the store's SealedRuns and
// the original grades: every run comes back checked against its seal, a
// run that does not verify with the reason in Problems.
func (x indexAdapter) VerifiedRuns(ctx context.Context, expID string) ([]schemas.SealedRun, error) {
	st, err := x.readStore()
	if err != nil {
		return nil, err
	}
	return st.SealedRuns(ctx, expID, store.GradeSourceOriginal)
}

// RunEvidence implements clusters.Source. It reads only the files in
// clusterEvidenceFiles, only regular files, from the indexed run folder.
func (x indexAdapter) RunEvidence(runID, name string) ([]byte, error) {
	if !clusterEvidenceFiles[name] {
		return nil, fmt.Errorf("run file %q: not read by clusters: %w", name, schemas.ErrNotFound)
	}
	e, ok := x.ix.Run(runID)
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	p := filepath.Join(e.Dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s file %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, clusterEvidenceMax))
	if err != nil {
		return nil, fmt.Errorf("run %s file %s: %w", runID, name, err)
	}
	return data, nil
}
