package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/maximhq/bifrost/bench/utils"
)

func init() {
	register("export", "export sealed runs for other tools: otlp writes OpenTelemetry traces (docs/trace-export.md)", runExport)
}

const exportUsage = `usage: bifrost-bench export otlp -exp <exp_id> [-run <run_id>] [-out file|dir/] [-payloads]
                                [-endpoint <url> [-header-env NAME]] [-runs <runs root> | -config deploy/bench.yaml]
Writes each sealed run as an OpenTelemetry trace in OTLP/JSON (docs/trace-export.md): to stdout,
to one file, or to one <run_id>.otlp.json per run in a folder (an existing folder or a path ending
in /). -endpoint also posts each run to an OTLP/HTTP collector; -header-env names an environment
variable holding its headers in OTEL_EXPORTER_OTLP_HEADERS format (k=v,k2=v2).`

// exportedTrace is one run's trace.
type exportedTrace struct {
	runID string
	td    *traceexport.TracesData
}

func runExport(args []string) int {
	if len(args) == 0 || args[0] != "otlp" {
		fmt.Fprintln(os.Stderr, exportUsage)
		return exitUsage
	}
	return runExportOTLP(args[1:], os.Stdout, os.Stderr)
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
	if err := fs.Parse(args); err != nil || *exp == "" || fs.NArg() != 0 || (*headerEnv != "" && *endpoint == "") {
		fmt.Fprintln(stderr, exportUsage)
		return exitUsage
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
	opts := traceexport.Options{Payloads: *payloads, ServiceVersion: benchVersion}
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
	var picked []store.RunEntry
	for _, e := range entries {
		switch {
		case runID != "" && e.Run.RunID != runID:
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
