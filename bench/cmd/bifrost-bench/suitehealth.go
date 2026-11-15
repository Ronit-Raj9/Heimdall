package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskhealth"
)

// suiteSubcommands are `suite` subcommands that live in their own files
// (suite health, quarantine, restore, canary, canary-check). Each file adds
// its entries from its own init; runSuite dispatches to them before its
// built-in subcommands. Package-level variables are initialized before any
// init runs, so the map always exists when an init writes to it.
var suiteSubcommands = map[string]func(args []string) int{}

func init() {
	suiteSubcommands["health"] = runSuiteHealth
	suiteSubcommands["quarantine"] = runSuiteQuarantine
	suiteSubcommands["restore"] = runSuiteRestore
}

const suiteHealthUsage = `usage: bifrost-bench suite health [-config deploy/bench.yaml | -root benchmarks -runs runs] [-json] [-auto-quarantine] [-min-runs 6] [-min-configs 2] <suite id>
       bifrost-bench suite quarantine [-config deploy/bench.yaml | -root benchmarks] -reason "..." <suite id>/<task id>
       bifrost-bench suite restore [-config deploy/bench.yaml | -root benchmarks] <suite id>/<task id>`

// runSuiteHealth computes and prints the health of every task of a suite
// from the sealed runs of every experiment in the run store. With
// -auto-quarantine it quarantines the tasks with verifier flakiness evidence,
// and only those: difficulty, saturation and agent variance never quarantine.
func runSuiteHealth(args []string) int {
	fs := flag.NewFlagSet("suite health", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	runs := fs.String("runs", "", "run store folder (default: runs_root of the config)")
	config := fs.String("config", defaultConfig, "bench config")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	auto := fs.Bool("auto-quarantine", false, "quarantine tasks with verifier flakiness evidence (never on difficulty)")
	minRuns := fs.Int("min-runs", taskhealth.DefaultOptions.MinRuns, "scored non-synthetic runs before difficulty is labelled")
	minConfigs := fs.Int("min-configs", taskhealth.DefaultOptions.MinConfigs, "distinct agent configs before difficulty is labelled")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteHealthUsage)
		return exitUsage
	}
	benchRoot, cfg, err := benchmarksRoot(fs, *root, *config)
	if err != nil {
		return fail("%v", err)
	}
	runsRoot := *runs
	if runsRoot == "" {
		if cfg == nil {
			return fail("suite health: no run store; pass -runs <folder> or a readable -config")
		}
		runsRoot = cfg.Path(cfg.RunsRoot)
	}
	ctx := context.Background()
	s, err := loadSuite(ctx, benchRoot, pos[0])
	if err != nil {
		return fail("load suite %s: %v", pos[0], err)
	}
	src, err := openHealthSource(runsRoot)
	if err != nil {
		return fail("%v", err)
	}
	hf, err := suite.ReadHealthFile(s.Root)
	if err != nil {
		return fail("%v", err)
	}
	h, err := taskhealth.Compute(ctx, src, s, hf.Quarantined, taskhealth.Options{MinRuns: *minRuns, MinConfigs: *minConfigs})
	if err != nil {
		return fail("%v", err)
	}
	if *auto {
		if err := autoQuarantine(s, h, hf.Quarantined, os.Stderr); err != nil {
			return fail("%v", err)
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(h); err != nil {
			return fail("encode health: %v", err)
		}
		return exitOK
	}
	printHealth(os.Stdout, h)
	return exitOK
}

// runSuiteQuarantine records a task as quarantined in suite.health.json. The
// lock is not touched, so its hash and every manifest written before stay
// valid; only new plans skip the task.
func runSuiteQuarantine(args []string) int {
	fs := flag.NewFlagSet("suite quarantine", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	reason := fs.String("reason", "", "why the task is quarantined (required)")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 || strings.TrimSpace(*reason) == "" {
		fmt.Fprintln(os.Stderr, suiteHealthUsage)
		return exitUsage
	}
	s, taskID, code := suiteTaskArg(fs, *root, *config, pos[0])
	if s == nil {
		return code
	}
	if err := suite.Quarantine(s, taskID, schemas.TaskQuarantine{Reason: *reason, Source: schemas.QuarantineManual}); err != nil {
		return fail("%v", err)
	}
	fmt.Printf("quarantined %s/%s: %s\nnew plans skip it under tasks: all and refuse it by name without -include-quarantined; suite.lock and existing experiments are unchanged (%s)\n",
		s.ID, taskID, *reason, filepath.Join(s.Root, schemas.SuiteHealthFileName))
	return exitOK
}

// runSuiteRestore takes a task out of quarantine.
func runSuiteRestore(args []string) int {
	fs := flag.NewFlagSet("suite restore", flag.ContinueOnError)
	root := fs.String("root", "", "benchmarks folder (default: benchmarks_root of the config, else benchmarks)")
	config := fs.String("config", defaultConfig, "bench config")
	pos, err := parseArgs(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(os.Stderr, suiteHealthUsage)
		return exitUsage
	}
	s, taskID, code := suiteTaskArg(fs, *root, *config, pos[0])
	if s == nil {
		return code
	}
	ok, err := suite.Restore(s, taskID)
	if err != nil {
		return fail("%v", err)
	}
	if !ok {
		fmt.Printf("%s/%s was not quarantined\n", s.ID, taskID)
		return exitOK
	}
	fmt.Printf("restored %s/%s: new plans include it again\n", s.ID, taskID)
	return exitOK
}

// suiteTaskArg loads the suite of a "<suite>/<task>" argument. On failure it
// returns a nil suite and the exit code.
func suiteTaskArg(fs *flag.FlagSet, root, config, arg string) (*schemas.Suite, string, int) {
	suiteID, taskID, ok := strings.Cut(arg, "/")
	if !ok || suiteID == "" || taskID == "" {
		fmt.Fprintf(os.Stderr, "want <suite id>/<task id>, got %q\n%s\n", arg, suiteHealthUsage)
		return nil, "", exitUsage
	}
	benchRoot, _, err := benchmarksRoot(fs, root, config)
	if err != nil {
		return nil, "", fail("%v", err)
	}
	s, err := loadSuite(context.Background(), benchRoot, suiteID)
	if err != nil {
		return nil, "", fail("load suite %s: %v", suiteID, err)
	}
	if s.TaskByID(taskID) == nil {
		return nil, "", fail("task %s is not in suite %s", taskID, s.ID)
	}
	return s, taskID, exitOK
}

// autoQuarantine quarantines every task with verifier flakiness evidence
// that is not quarantined yet, recording the evidence lines.
func autoQuarantine(s *schemas.Suite, h *schemas.SuiteHealth, already map[string]schemas.TaskQuarantine, w io.Writer) error {
	for _, th := range h.Tasks {
		if !th.Verifier.Flaky {
			continue
		}
		if _, ok := already[th.Task]; ok {
			continue
		}
		kinds := map[string]bool{}
		var names []string
		for _, e := range th.Verifier.Evidence {
			if !kinds[e.Kind] {
				kinds[e.Kind] = true
				names = append(names, e.Kind)
			}
		}
		q := schemas.TaskQuarantine{Reason: "auto: verifier flakiness (" + strings.Join(names, ", ") + ")",
			Source: schemas.QuarantineAuto, Evidence: taskhealth.EvidenceLines(th.Verifier), At: time.Now().UTC()}
		if err := suite.Quarantine(s, th.Task, q); err != nil {
			return err
		}
		fmt.Fprintf(w, "auto-quarantined %s/%s: %s\n", s.ID, th.Task, q.Reason)
		for i := range h.Tasks {
			if h.Tasks[i].Task == th.Task {
				h.Tasks[i].Quarantine = &q
			}
		}
	}
	return nil
}

// printHealth writes the human report: one row per task, then the verifier
// evidence and the notes. Verifier flakiness and agent variance are separate
// columns on purpose.
func printHealth(w io.Writer, h *schemas.SuiteHealth) {
	fmt.Fprintf(w, "%s@%s health: %d experiments, %d sealed attempts (difficulty needs %d scored runs over %d configs; synthetic runs left out)\n\n",
		h.Suite, h.Version, h.Experiments, h.Runs, h.Thresholds.MinRuns, h.Thresholds.MinConfigs)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TASK\tSPLIT\tVERIFIER\tAGENT VARIANCE\tINFRA\tTIMEOUTS\tPASS RATE (95% CI)\tRUNS/CONFIGS\tDIFFICULTY obs/decl\tDISCRIMINATION\tSTATE")
	for _, t := range h.Tasks {
		verifier := fmt.Sprintf("ok (%d checks)", t.Verifier.Checks)
		if t.Verifier.Flaky {
			verifier = fmt.Sprintf("FLAKY (%d evidence)", len(t.Verifier.Evidence))
		}
		variance := fmt.Sprintf("%d/%d configs mixed", t.Variance.Unstable, t.Variance.Configs)
		rate := "-"
		if d := t.Difficulty; d.PassRate != nil && d.Interval != nil {
			rate = fmt.Sprintf("%.2f [%.2f, %.2f]", *d.PassRate, d.Interval[0], d.Interval[1])
		}
		declared := t.Difficulty.Declared
		if declared == "" {
			declared = "-"
		}
		state := "active"
		if t.Quarantine != nil {
			state = "quarantined: " + t.Quarantine.Reason
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d/%d\t%s/%s\t%s\t%s\n", t.Task, t.Split, verifier, variance,
			pct(t.Infra.Rate, t.Infra.Attempts), pct(t.Infra.TimeoutRate, t.Infra.Attempts), rate,
			t.Difficulty.Runs, t.Difficulty.Configs, t.Difficulty.Observed, declared, t.Difficulty.Discrimination, state)
	}
	tw.Flush()
	for _, t := range h.Tasks {
		if len(t.Flags) > 0 {
			fmt.Fprintf(w, "\n%s: %s", t.Task, strings.Join(t.Flags, ", "))
		}
		for _, line := range taskhealth.EvidenceLines(t.Verifier) {
			fmt.Fprintf(w, "\n  verifier evidence: %s", line)
		}
	}
	fmt.Fprintln(w)
	for _, n := range h.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	if flaky := h.Counts[schemas.HealthFlakyVerifier]; flaky > 0 {
		fmt.Fprintf(w, "\n%d task(s) with a flaky verifier: their verdicts cannot be trusted. Fix the verifier, or `suite quarantine` them (or rerun with -auto-quarantine).\n", flaky)
	}
}

func pct(rate float64, n int) string {
	if n == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%% of %d", rate*100, n)
}

// healthSource adapts the file store to taskhealth.Source. Regrades are
// listed from the experiment's regrades/ folder and read with the store's
// regrade.json reader, as the leaderboard does.
type healthSource struct {
	st *store.FSStore
}

// openHealthSource opens the run store at root for reading.
func openHealthSource(root string) (*healthSource, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("run store %s: %w", root, err)
	}
	st, err := store.NewFSStore(root, schemas.NoopLogger{})
	if err != nil {
		return nil, fmt.Errorf("open run store %s: %w", root, err)
	}
	return &healthSource{st: st}, nil
}

// serveHealthSource is the health source the server uses, or a nil
// interface when the run store cannot be opened, so the health route
// answers 503 instead of failing the server.
func serveHealthSource(cfg *lib.Config) taskhealth.Source {
	if cfg == nil {
		return nil
	}
	src, err := openHealthSource(cfg.Path(cfg.RunsRoot))
	if err != nil {
		return nil
	}
	return src
}

// Experiments implements taskhealth.Source.
func (h *healthSource) Experiments(ctx context.Context) ([]string, error) {
	return h.st.ListExperiments(ctx)
}

// Manifest implements taskhealth.Source.
func (h *healthSource) Manifest(ctx context.Context, expID string) (*schemas.Manifest, error) {
	return h.st.LoadManifest(ctx, expID)
}

// SealedRuns implements taskhealth.Source.
func (h *healthSource) SealedRuns(ctx context.Context, expID, gradeSource string) ([]schemas.SealedRun, error) {
	return h.st.SealedRuns(ctx, expID, gradeSource)
}

// SlotAttempts implements taskhealth.Source.
func (h *healthSource) SlotAttempts(ctx context.Context, expID, slotID string) ([]schemas.RunRecord, error) {
	return h.st.SlotRuns(ctx, expID, slotID)
}

// Regrades implements taskhealth.Source. A folder without regrade.json (a
// regrade interrupted before its first write) is skipped.
func (h *healthSource) Regrades(ctx context.Context, expID string) ([]taskhealth.Regrade, error) {
	entries, err := os.ReadDir(filepath.Join(h.st.Root(), expID, regradesDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list regrades of %s: %w", expID, err)
	}
	var out []taskhealth.Regrade
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := h.st.RegradeInfo(ctx, expID, e.Name())
		if errors.Is(err, schemas.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, taskhealth.Regrade{ID: e.Name(), SuiteVersion: info.SuiteVersion, GraderVersion: info.GraderVersion})
	}
	return out, nil
}

// includeQuarantined is the -include-quarantined flag of `plan` and `run`:
// an explicit task list may then name a quarantined task. One process runs
// one command, so a package variable set by that command's flag set is
// enough, and newPlanner reads it.
var includeQuarantined bool
