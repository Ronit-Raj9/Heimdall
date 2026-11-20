package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/export"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("export", "export for other tools: otlp traces (with backend presets), harbor tasks, eee results, jsonl/csv run tables (docs/export.md)", runExport)
}

const exportUsage = `usage: bifrost-bench export otlp -exp <exp_id> [-run <run_id>] [-out file|dir/] [-payloads]
                                [-to <preset>] [-endpoint <url> [-header-env NAME]] [-runs <runs root> | -config deploy/bench.yaml]
Writes each sealed run as an OpenTelemetry trace in OTLP/JSON (docs/trace-export.md): to stdout,
skipping held-out task runs unless -include-held-out asks for a private export;
to one file, or to one <run_id>.otlp.json per run in a folder (an existing folder or a path ending
in /). -endpoint also posts each run to an OTLP/HTTP collector; -header-env names an environment
variable holding its headers in OTEL_EXPORTER_OTLP_HEADERS format (k=v,k2=v2).
-to langfuse|langsmith|braintrust|datadog|maxim|phoenix|jaeger posts with that backend's documented
traces URL (or -endpoint), headers built from its environment variables, encoding and span
attribute mapping (docs/export.md lists each, verified and not).

       bifrost-bench export harbor -suite <id[@version]> [-tasks t1,t2] -out <dir> -include-hidden [-admitted-only] [-force]
Writes each task as a Harbor task folder. A Harbor task carries its verifier, so the export holds
the hidden tests and reference solutions: -include-hidden is required. Held-out tasks are never
exported; tasks the format cannot hold honestly are skipped with the reason.

       bifrost-bench export eee -exp <exp_id> [-out file|dir/] [-org <name>] [-relationship third_party|first_party|collaborative|other]
                                [-allow-synthetic] [-grade-source original|<regrade id>] [-runs <root> | -config ...]
Writes one Every Eval Ever aggregate record per arm (agent config, model, suite), validated against
the vendored schema: one JSON object per line to stdout, one file, or one <exp>-<arm>.json per arm.

       bifrost-bench export jsonl|csv -exp <exp_id> [-out file] [-grade-source ...] [-split public|held_out|all] [-runs <root> | -config ...]
Writes the flat run table: one row per sealed counted run with ids, outcome, cost and metrics.
Rows of held-out tasks are left out unless -split held_out or all asks for a private export; each
row names its split. An experiment whose newest verdict is INVALID is never exported as eee.

       bifrost-bench export inspect
Not written; prints why (docs/export.md).`

// exportedTrace is one run's trace.
type exportedTrace struct {
	runID string
	td    *traceexport.TracesData
}

func runExport(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, exportUsage)
		return exitUsage
	}
	switch args[0] {
	case "otlp":
		return runExportOTLP(args[1:], os.Stdout, os.Stderr)
	case "harbor", "eee", "jsonl", "csv", "inspect": // W10 export formats (exportformats in this file)
		return runExportFormat(args[0], args[1:], os.Stdout, os.Stderr)
	}
	fmt.Fprintln(os.Stderr, exportUsage)
	return exitUsage
}

func runExportOTLP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export otlp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", defaultConfig, "bench config, for its runs_root")
	runsRoot := fs.String("runs", "", "runs root to read (default: runs_root of the config)")
	exp := fs.String("exp", "", "experiment id")
	run := fs.String("run", "", "one run id (default: every sealed run of the experiment)")
	out := fs.String("out", "", "file, or folder for one file per run (default: stdout, unless -endpoint is set)")
	payloads := fs.Bool("payloads", false, "include tool arguments and results, the answer and the claim text, redacted and capped")
	endpoint := fs.String("endpoint", "", "OTLP/HTTP collector to post each run to (a base URL gets /v1/traces)")
	headerEnv := fs.String("header-env", "", "environment variable holding the collector headers, OTEL_EXPORTER_OTLP_HEADERS format")
	to := fs.String("to", "", "backend preset: "+strings.Join(export.PresetNames(), ", "))
	includeHeldOut := fs.Bool("include-held-out", false, "also export runs of held-out tasks (a private export; default: skipped)")
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 || (*headerEnv != "" && *endpoint == "" && *to == "") {
		fmt.Fprintln(stderr, exportUsage)
		return exitUsage
	}
	if *to != "" {
		return runExportOTLPPreset(*to, *config, *runsRoot, *exp, *run, *out, *endpoint, *headerEnv, *payloads, stdout, stderr)
	}
	var tracesURL string
	var headers map[string]string
	if *endpoint != "" {
		var err error
		if tracesURL, err = traceexport.TracesURL(*endpoint); err != nil {
			return failTo(stderr, "%v", err)
		}
		if headers, err = envHeaders(*headerEnv); err != nil {
			return failTo(stderr, "%v", err)
		}
	}
	root := *runsRoot
	if root == "" {
		cfg, err := lib.LoadConfig(*config)
		if err != nil {
			return failTo(stderr, "%v", err)
		}
		root = cfg.Path(cfg.RunsRoot)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	opts := traceexport.Options{Payloads: *payloads, ServiceVersion: benchVersion, IncludeHeldOut: *includeHeldOut}
	traces, err := exportOTLP(ctx, root, *exp, *run, opts, stderr)
	if err != nil {
		return failTo(stderr, "export otlp: %v", err)
	}
	if *out != "" || tracesURL == "" {
		if err := writeTraces(*out, traces, stdout, stderr); err != nil {
			return failTo(stderr, "export otlp: %v", err)
		}
	}
	if tracesURL != "" {
		if err := postTraces(ctx, nil, tracesURL, headers, traces, stderr); err != nil {
			return failTo(stderr, "export otlp: %v", err)
		}
	}
	return exitOK
}

// exportOTLP builds the trace of one run, or of every sealed run of an
// experiment, from the runs root. Every run's seal is verified first, and a
// run that does not verify fails the whole export before anything is
// written. A named run that is not sealed is refused; when exporting a whole
// experiment, runs not yet sealed are skipped with a note on stderr.
func exportOTLP(ctx context.Context, root, expID, runID string, opts traceexport.Options, stderr io.Writer) ([]exportedTrace, error) {
	st, err := store.NewFSStore(root, utils.NewStdLogger(stderr, false))
	if err != nil {
		return nil, err
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		return nil, err
	}
	entries, ok := ix.Runs(expID)
	if !ok {
		return nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return nil, err
	}
	var picked []store.RunEntry
	for _, e := range entries {
		held := !opts.IncludeHeldOut && slices.Contains(m.HeldOutTasks, e.Slot.Task)
		switch {
		case runID != "" && e.Run.RunID != runID:
		case held && runID != "":
			return nil, fmt.Errorf("run %s: task %s is held out; pass -include-held-out for a private export", runID, e.Slot.Task)
		case held:
			fmt.Fprintf(stderr, "skipped run %s: task %s is held out\n", e.Run.RunID, e.Slot.Task)
		case !e.Sealed && runID != "":
			return nil, fmt.Errorf("run %s: %w: only sealed runs are exported", runID, traceexport.ErrUnsealed)
		case !e.Sealed:
			fmt.Fprintf(stderr, "skipped run %s: not sealed\n", e.Run.RunID)
		default:
			picked = append(picked, e)
		}
	}
	if len(picked) == 0 {
		if runID != "" {
			return nil, fmt.Errorf("run %s of experiment %s: %w", runID, expID, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("experiment %s has no sealed runs", expID)
	}
	for _, e := range picked {
		if err := st.Verify(ctx, expID, e.Run.RunID); err != nil {
			return nil, fmt.Errorf("run %s: seal does not verify, nothing exported: %w", e.Run.RunID, err)
		}
	}
	out := make([]exportedTrace, 0, len(picked))
	for _, e := range picked {
		in, err := traceexport.LoadRun(e.Dir)
		if err != nil {
			return nil, err
		}
		td, err := traceexport.Build(in, opts)
		if err != nil {
			return nil, err
		}
		out = append(out, exportedTrace{runID: e.Run.RunID, td: td})
	}
	return out, nil
}

// writeTraces writes the traces: one merged document to stdout (out empty)
// or to the file out, or one <run_id>.otlp.json per run when out is an
// existing folder or ends in a path separator.
func writeTraces(out string, traces []exportedTrace, stdout, stderr io.Writer) error {
	if out == "" {
		return writeMerged(stdout, traces)
	}
	info, statErr := os.Stat(out)
	isDir := strings.HasSuffix(out, "/") || strings.HasSuffix(out, string(filepath.Separator)) || (statErr == nil && info.IsDir())
	if !isDir {
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		if err := writeMerged(f, traces); err != nil {
			_ = f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "wrote %s (%d runs)\n", out, len(traces))
		return nil
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, tr := range traces {
		b, err := traceexport.Marshal(tr.td)
		if err != nil {
			return err
		}
		p := filepath.Join(out, tr.runID+".otlp.json")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "wrote %s\n", p)
	}
	return nil
}

// writeMerged writes every trace as one OTLP/JSON document.
func writeMerged(w io.Writer, traces []exportedTrace) error {
	tds := make([]*traceexport.TracesData, len(traces))
	for i, tr := range traces {
		tds[i] = tr.td
	}
	b, err := traceexport.Marshal(traceexport.Merge(tds...))
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// postTraces posts each run's trace to the collector, one request per run,
// and stops at the first failure.
func postTraces(ctx context.Context, client *http.Client, tracesURL string, headers map[string]string, traces []exportedTrace, stderr io.Writer) error {
	for _, tr := range traces {
		b, err := traceexport.Marshal(tr.td)
		if err != nil {
			return err
		}
		if err := traceexport.Post(ctx, client, tracesURL, headers, b); err != nil {
			return fmt.Errorf("run %s: %w", tr.runID, err)
		}
		fmt.Fprintf(stderr, "posted run %s (trace %s)\n", tr.runID, traceexport.TraceID(tr.runID))
	}
	return nil
}

// envHeaders reads collector headers from the environment variable name. An
// empty name means no headers; a named variable that is unset or empty is an
// error, so a missing credential is never sent as nothing in silence.
func envHeaders(name string) (map[string]string, error) {
	if name == "" {
		return nil, nil
	}
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return nil, fmt.Errorf("header variable %s is not set", name)
	}
	h, err := traceexport.ParseHeaders(v)
	if err != nil {
		return nil, fmt.Errorf("header variable %s: %w", name, err)
	}
	if len(h) == 0 {
		return nil, errors.New("header variable " + name + " holds no headers")
	}
	return h, nil
}

// failTo prints an error to w and returns the infrastructure exit code.
func failTo(w io.Writer, format string, args ...any) int {
	fmt.Fprintf(w, "error: "+format+"\n", args...)
	return exitInfra
}

// W10 export formats: OTLP backend presets, Harbor tasks, Every Eval Ever
// records and the flat run table (bench/export, docs/export.md).

// inspectSkipReason says why there is no Inspect log export.
const inspectSkipReason = `export inspect: not written. Inspect's log format is defined by its Python classes
(EvalLog: eval spec, plan, results, stats, samples); its JSON schema comes from the
"inspect log schema" command, not a published file, and Inspect's docs say to read and
write logs through its Python API rather than as JSON. Heimdall runs are not Inspect
samples (no solver plan, no scorer), so a log would be mostly invented fields.
Use "export eee": Every Eval Ever is the interchange format Inspect results are
converted into. See docs/export.md.`

// runExportOTLPPreset exports traces with a backend preset: the preset's
// attribute mapping and encoding, posted to its traces URL (or endpoint)
// with headers built from its environment variables plus any -header-env
// headers, and written to -out as OTLP/JSON when asked.
func runExportOTLPPreset(name, config, runsRoot, expID, runID, out, endpoint, headerEnv string, payloads bool, stdout, stderr io.Writer) int {
	p, ok := export.Presets()[name]
	if !ok {
		fmt.Fprintf(stderr, "export otlp: unknown preset %q (known: %s)\n", name, strings.Join(export.PresetNames(), ", "))
		return exitUsage
	}
	tracesURL := cmp.Or(presetEndpoint(endpoint, p.Endpoint), p.Endpoint)
	if tracesURL == "" {
		fmt.Fprintf(stderr, "export otlp -to %s: the preset has no default endpoint; pass -endpoint (%s)\n", name, strings.Join(p.Unverified, "; "))
		return exitUsage
	}
	var err error
	if tracesURL, err = traceexport.TracesURL(tracesURL); err != nil {
		return failTo(stderr, "%v", err)
	}
	headers, err := p.PresetHeaders(nil)
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	extra, err := envHeaders(headerEnv)
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	for k, v := range extra {
		headers[k] = v
	}
	root, err := runsRootOf(config, runsRoot)
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	traces, err := exportOTLP(ctx, root, expID, runID, traceexport.Options{Payloads: payloads, ServiceVersion: benchVersion}, stderr)
	if err != nil {
		return failTo(stderr, "export otlp: %v", err)
	}
	for _, tr := range traces {
		p.Apply(tr.td)
	}
	if out != "" {
		if err := writeTraces(out, traces, stdout, stderr); err != nil {
			return failTo(stderr, "export otlp: %v", err)
		}
	}
	for _, tr := range traces {
		body, ctype, err := p.Encode(tr.td)
		if err != nil {
			return failTo(stderr, "export otlp: run %s: %v", tr.runID, err)
		}
		if err := export.PostTrace(ctx, nil, tracesURL, headers, body, ctype); err != nil {
			return failTo(stderr, "export otlp: run %s: %v", tr.runID, err)
		}
		fmt.Fprintf(stderr, "posted run %s to %s (%s, trace %s)\n", tr.runID, name, ctype, traceexport.TraceID(tr.runID))
	}
	return exitOK
}

// runsRootOf is -runs, or the config's runs_root.
func runsRootOf(config, runsRoot string) (string, error) {
	if runsRoot != "" {
		return runsRoot, nil
	}
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		return "", err
	}
	return cfg.Path(cfg.RunsRoot), nil
}

// runExportFormat runs export harbor, eee, jsonl, csv and inspect.
func runExportFormat(format string, args []string, stdout, stderr io.Writer) int {
	if format == "inspect" {
		fmt.Fprintln(stderr, inspectSkipReason)
		return exitUsage
	}
	fs := flag.NewFlagSet("export "+format, flag.ContinueOnError)
	fs.SetOutput(stderr)
	config := fs.String("config", defaultConfig, "bench config, for its runs_root and benchmarks_root")
	runsRoot := fs.String("runs", "", "runs root to read (default: runs_root of the config)")
	out := fs.String("out", "", "output file or folder")
	exp := fs.String("exp", "", "experiment id")
	gradeSource := fs.String("grade-source", schemas.GradeSourceOriginal, "original, or a regrade id of the experiment")
	suiteRef := fs.String("suite", "", "suite id or id@version (harbor)")
	tasks := fs.String("tasks", "", "comma-separated task ids (harbor; default every public task)")
	includeHidden := fs.Bool("include-hidden", false, "write the hidden tests and reference solutions a Harbor task needs (harbor)")
	admittedOnly := fs.Bool("admitted-only", false, "skip tasks the suite lock does not mark admitted (harbor)")
	force := fs.Bool("force", false, "replace task folders that exist (harbor)")
	org := fs.String("org", "", "source organization name written in each record (eee)")
	relationship := fs.String("relationship", "third_party", "evaluator relationship to the model developer (eee)")
	allowSynthetic := fs.Bool("allow-synthetic", false, "export synthetic arms, labelled (eee)")
	split := fs.String("split", schemas.SplitPublic, "jsonl|csv: public, held_out or all (a private export of held-out task rows); eee is always public")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(stderr, exportUsage)
		return exitUsage
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if format == "harbor" {
		if *suiteRef == "" || *out == "" {
			fmt.Fprintln(stderr, exportUsage)
			return exitUsage
		}
		return exportHarborCmd(ctx, *config, *suiteRef, *tasks, *out, *includeHidden, *admittedOnly, *force, stderr)
	}
	if *exp == "" {
		fmt.Fprintln(stderr, exportUsage)
		return exitUsage
	}
	root, err := runsRootOf(*config, *runsRoot)
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	if !export.ValidSplit(*split) || (format == "eee" && *split != schemas.SplitPublic) {
		fmt.Fprintf(stderr, "export %s: split %q: want public, held_out or all (eee: public only)\n", format, *split)
		return exitUsage
	}
	in, err := loadResults(ctx, root, *exp, *gradeSource, stderr)
	if err != nil {
		return failTo(stderr, "export %s: %v", format, err)
	}
	in.Split = *split
	if format == "eee" {
		recs, err := export.EEE(ctx, in, export.EEEOptions{Organization: *org, Relationship: *relationship, AllowSynthetic: *allowSynthetic,
			GradeSource: *gradeSource, BenchVersion: benchVersion})
		if errors.Is(err, export.ErrSynthetic) || errors.Is(err, export.ErrInvalid) {
			fmt.Fprintf(stderr, "export eee: %v\n", err)
			return exitUsage
		}
		if err != nil {
			return failTo(stderr, "%v", err)
		}
		if err := writeEEE(*out, *exp, recs, stdout, stderr); err != nil {
			return failTo(stderr, "export eee: %v", err)
		}
		return exitOK
	}
	rows := export.RunTable(in)
	write := export.WriteJSONL
	if format == "csv" {
		write = export.WriteCSV
	}
	if *out == "" {
		if err := write(stdout, rows); err != nil {
			return failTo(stderr, "export %s: %v", format, err)
		}
		return exitOK
	}
	var buf bytes.Buffer
	if err := write(&buf, rows); err != nil {
		return failTo(stderr, "export %s: %v", format, err)
	}
	if err := os.WriteFile(*out, buf.Bytes(), 0o644); err != nil {
		return failTo(stderr, "export %s: %v", format, err)
	}
	fmt.Fprintf(stderr, "wrote %s (%d runs)\n", *out, len(rows))
	return exitOK
}

// loadResults reads an experiment's manifest and sealed runs (each checked
// against its seal by SealedRuns) and marks synthetic arms.
func loadResults(ctx context.Context, root, expID, gradeSource string, stderr io.Writer) (export.EEEInput, error) {
	st, err := store.NewFSStore(root, utils.NewStdLogger(stderr, false))
	if err != nil {
		return export.EEEInput{}, err
	}
	return loadResultsFrom(ctx, st, expID, gradeSource)
}

// loadResultsFrom is loadResults over an open store.
func loadResultsFrom(ctx context.Context, st *store.FSStore, expID, gradeSource string) (export.EEEInput, error) {
	m, err := st.LoadManifest(ctx, expID)
	if err != nil {
		return export.EEEInput{}, err
	}
	runs, err := st.SealedRuns(ctx, expID, gradeSource)
	if err != nil {
		return export.EEEInput{}, err
	}
	synthetic, err := leaderboard.SyntheticArms(ctx, m, runs, sealedRunFile)
	if err != nil {
		return export.EEEInput{}, err
	}
	d, _ := (indexAdapter{root: st.Root()}).LatestDecision(expID)
	return export.EEEInput{Manifest: m, Runs: runs, Synthetic: synthetic, Invalid: d == schemas.DecisionInvalid}, nil
}

// writeEEE writes the records: JSON lines to stdout, one file (one record),
// or one <exp>-<arm>.json per record in a folder.
func writeEEE(out, expID string, recs []export.EEERecord, stdout, stderr io.Writer) error {
	if out == "" {
		enc := json.NewEncoder(stdout)
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
		return nil
	}
	info, statErr := os.Stat(out)
	isDir := strings.HasSuffix(out, "/") || (statErr == nil && info.IsDir())
	if !isDir && len(recs) != 1 {
		return fmt.Errorf("%d records (one per arm): -out must be a folder (end it in /)", len(recs))
	}
	if isDir {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return err
		}
	}
	for _, r := range recs {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		p := out
		if isDir {
			id, _ := r["evaluation_id"].(string)
			p = filepath.Join(out, expID+"-"+filepath.Base(id)+".json")
		}
		if err := os.WriteFile(p, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "wrote %s\n", p)
	}
	return nil
}

// exportHarborCmd writes the suite's tasks as Harbor task folders.
func exportHarborCmd(ctx context.Context, config, ref, tasks, out string, includeHidden, admittedOnly, force bool, stderr io.Writer) int {
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	s, err := loadSuite(ctx, cfg.Path(cfg.BenchmarksRoot), ref)
	if err != nil {
		return failTo(stderr, "export harbor: %v", err)
	}
	var ids []string
	for _, id := range strings.Split(tasks, ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	trees, res, err := export.PlanHarbor(s, export.HarborOptions{Tasks: ids, IncludeHidden: includeHidden, RequireAdmitted: admittedOnly,
		Exporter: "bifrost-bench export harbor " + benchVersion})
	if errors.Is(err, export.ErrHiddenTests) {
		fmt.Fprintf(stderr, "export harbor: %v\n", err)
		return exitUsage
	}
	if err != nil {
		return failTo(stderr, "%v", err)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	for _, sk := range res.Skipped {
		fmt.Fprintf(stderr, "skipped %s: %s\n", sk.ID, sk.Reason)
	}
	if len(res.Tasks) == 0 {
		return failTo(stderr, "export harbor: no task exported; see the skipped list")
	}
	if err := export.WriteHarborDir(out, trees, res, force); err != nil {
		return failTo(stderr, "export harbor: %v", err)
	}
	for _, t := range res.Tasks {
		fmt.Fprintf(stderr, "wrote %s (%s, %s)\n", filepath.Join(out, t.Folder), t.Name, t.Shape)
	}
	fmt.Fprintf(stderr, "next: harbor run -p %s -a oracle   (or: bifrost-bench import harbor -from %s -suite <id>)\n", out, out)
	return exitOK
}

// serveExport implements handlers.ExportSource for serve: results from the
// store (original grades, every seal checked), and Harbor tars with every
// verifier withheld and only admitted public tasks.
type serveExport struct{ a *app }

// ExperimentExport implements handlers.ExportSource.
func (s serveExport) ExperimentExport(ctx context.Context, expID, format string, allowSynthetic bool) ([]byte, string, error) {
	in, err := loadResultsFrom(ctx, s.a.store, expID, schemas.GradeSourceOriginal)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	switch format {
	case handlers.ExportFormatEEE:
		recs, err := export.EEE(ctx, in, export.EEEOptions{AllowSynthetic: allowSynthetic, GradeSource: schemas.GradeSourceOriginal, BenchVersion: benchVersion})
		if errors.Is(err, export.ErrSynthetic) || errors.Is(err, export.ErrInvalid) {
			return nil, "", fmt.Errorf("%w: %v", handlers.ErrExportRefused, err)
		}
		if err != nil {
			return nil, "", err
		}
		enc := json.NewEncoder(&buf)
		for _, r := range recs {
			if err := enc.Encode(r); err != nil {
				return nil, "", err
			}
		}
		return buf.Bytes(), "application/x-ndjson", nil
	case handlers.ExportFormatJSONL:
		err = export.WriteJSONL(&buf, export.RunTable(in))
		return buf.Bytes(), "application/x-ndjson", err
	case handlers.ExportFormatCSV:
		err = export.WriteCSV(&buf, export.RunTable(in))
		return buf.Bytes(), "text/csv; charset=utf-8", err
	}
	return nil, "", fmt.Errorf("format %q: %w", format, schemas.ErrNotFound)
}

// SuiteHarbor implements handlers.ExportSource.
func (s serveExport) SuiteHarbor(ctx context.Context, suiteID string) ([]byte, error) {
	st, err := loadSuite(ctx, s.a.cfg.Path(s.a.cfg.BenchmarksRoot), suiteID)
	if err != nil {
		return nil, fmt.Errorf("suite %s: %w: %v", suiteID, schemas.ErrNotFound, err)
	}
	trees, res, err := export.PlanHarbor(st, export.HarborOptions{Withhold: true, RequireAdmitted: true, Exporter: "bifrost-bench serve " + benchVersion})
	if err != nil {
		return nil, err
	}
	if len(res.Tasks) == 0 {
		return nil, fmt.Errorf("%w: no admitted public task of suite %s can be exported", handlers.ErrExportRefused, suiteID)
	}
	var buf bytes.Buffer
	if err := export.WriteHarborTar(&buf, suiteID, trees, res); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// presetEndpoint gives a base -endpoint (no path) the preset's own traces
// path, so -endpoint https://us.cloud.langfuse.com reaches
// /api/public/otel/v1/traces and a Maxim base reaches /v1/otel; an endpoint
// with a path is used as given.
func presetEndpoint(endpoint, preset string) string {
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Path != "" && u.Path != "/") {
		return endpoint
	}
	if pu, err := url.Parse(preset); err == nil && pu.Path != "" {
		u.Path = pu.Path
		return u.String()
	}
	return endpoint
}
