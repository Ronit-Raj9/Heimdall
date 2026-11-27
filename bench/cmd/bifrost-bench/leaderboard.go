package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/verdict"
)

const leaderboardUsage = `usage: bifrost-bench leaderboard [-config deploy/bench.yaml] [-suite id@version] [-condition clean] [-scope all|common] [-synthetic exclude|only] [-grade-source original|latest|reviewed] [-k n] [-format md|json] [-out file]
Ranks agent configurations by verified success over every sealed run of one suite version under one condition, across experiments. Reads the run folder only; no gateway is needed.`

// regradesDir is the folder of an experiment that holds its regrades.
const regradesDir = "regrades"

var _ handlers.RegradeIndex = indexAdapter{}

func init() {
	register("leaderboard", "rank agent configs across experiments by verified success, with tiers", runLeaderboard)
}

func runLeaderboard(args []string) int {
	fs := flag.NewFlagSet("leaderboard", flag.ContinueOnError)
	config := fs.String("config", defaultConfig, "bench config")
	suiteFlag := fs.String("suite", "", "suite id@version (default: the newest suite with runs)")
	condition := fs.String("condition", schemas.ConditionClean, "condition: clean or a probe id")
	scope := fs.String("scope", leaderboard.ScopeAll, "all: each entry over its own tasks; common: every entry over the common tasks")
	synthetic := fs.String("synthetic", leaderboard.SyntheticExclude, "exclude: live runs only; only: scripted agent or scripted model runs only")
	gradeSource := fs.String("grade-source", leaderboard.GradeSourceOriginal, "original: runs as sealed; latest: each experiment's newest regrade; reviewed: runs as sealed with human reviews applied")
	k := fs.Int("k", 0, "k of the pass^k reliability for every entry, 2 or more (default: per entry, min(3, trials per task))")
	format := fs.String("format", "md", "md or json")
	// Suite health area: the held-out board is separate (leaderboard/split.go).
	split := fs.String("split", "public", "public: public tasks only; held_out: the separate held-out board")
	out := fs.String("out", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, leaderboardUsage)
		return exitUsage
	}
	if *format != "md" && *format != "json" {
		fmt.Fprintf(os.Stderr, "format %q is not md or json\n%s\n", *format, leaderboardUsage)
		return exitUsage
	}
	q := leaderboard.Query{
		Suite: *suiteFlag, Condition: *condition, Scope: *scope, Synthetic: *synthetic, GradeSource: *gradeSource, K: *k,
		Split: *split,
	}
	if _, err := q.Normalize(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, leaderboardUsage)
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
	src := handlers.LeaderboardSource(indexAdapter{ix: ix, root: a.store.Root()})
	sess := leaderboard.NewSession(src) // both boards from one collect
	lb, err := sess.Build(ctx, q)
	if errors.Is(err, leaderboard.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, leaderboardUsage)
		return exitUsage
	}
	if err != nil {
		return fail("leaderboard: %v", err)
	}
	var synth *schemas.Leaderboard
	if len(lb.Entries) == 0 && q.Synthetic != leaderboard.SyntheticOnly {
		sq := q
		sq.Synthetic = leaderboard.SyntheticOnly
		if synth, err = sess.Build(ctx, sq); err != nil {
			return fail("leaderboard: %v", err)
		}
	}
	for _, h := range leaderboardHints(*suiteFlag, lb, synth) {
		fmt.Fprintln(os.Stderr, h)
	}
	var body []byte
	if *format == "json" {
		if body, err = json.MarshalIndent(lb, "", "  "); err != nil {
			return fail("encode leaderboard: %v", err)
		}
		body = append(body, '\n')
	} else {
		body = []byte(leaderboard.Markdown(lb))
	}
	if *out == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			return fail("write leaderboard: %v", err)
		}
		return exitOK
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		return fail("write leaderboard: %v", err)
	}
	return exitOK
}

// leaderboardHints are the notes printed on stderr beside a board: which
// suites have runs when the board picked one by itself, and, when a live
// board is empty but scripted runs exist (a new project has only oracle,
// noop or smoke runs), that -synthetic only shows them. synth is the
// synthetic board for the same query, or nil. It is pure.
func leaderboardHints(suiteFlag string, lb, synth *schemas.Leaderboard) []string {
	var hints []string
	if suiteFlag == "" && len(lb.Suites) > 1 {
		hints = append(hints, fmt.Sprintf("suites with runs: %s; this board is %s, pick another with -suite <id@version>",
			strings.Join(lb.Suites, ", "), lb.Suite))
	}
	if len(lb.Entries) == 0 && len(lb.Excluded) > 0 {
		// Live runs exist but none could be ranked (live test l13: a live
		// experiment with an INVALID verdict was reported as "no live runs").
		parts := make([]string, 0, len(lb.Excluded))
		for _, x := range lb.Excluded {
			parts = append(parts, fmt.Sprintf("%s: %d runs (%s)", x.Reason, x.Runs, strings.Join(x.Experiments, ", ")))
		}
		hints = append(hints, fmt.Sprintf("no live runs ranked on %s: every live run was excluded; %s", lb.Suite, strings.Join(parts, "; ")))
	}
	if synth != nil && len(lb.Entries) == 0 && len(synth.Suites) > 0 {
		format := "no live runs to rank; scripted runs (control agents or the scripted model) exist for %s: add -synthetic only to see them"
		if len(lb.Excluded) > 0 {
			format = "scripted runs (control agents or the scripted model) also exist for %s: add -synthetic only to see them"
		}
		hints = append(hints, fmt.Sprintf(format, strings.Join(synth.Suites, ", ")))
	}
	return hints
}

// LatestDecision returns the verdict decision of the experiment's newest
// complete analysis, so the leaderboard can leave out experiments whose
// verdict is invalid. Unreadable analysis state fails closed and reads as
// INVALID: it might be the newest record that said so (audit 1-D-07).
func (x indexAdapter) LatestDecision(expID string) (schemas.Decision, bool) {
	a, err := x.latestAnalysis(expID)
	if err != nil {
		return schemas.DecisionInvalid, true
	}
	if a == nil || a.Verdict.Decision == "" {
		return "", false
	}
	return a.Verdict.Decision, true
}

// LatestInvalid implements leaderboard.AnalysisSource (fix wave 3, N215):
// the invalid scope and reasons of the newest analysis when it decided
// INVALID. An analysis stored before the scope existed reads as
// "experiment"; an unreadable one is not reported here (LatestDecision
// already fails closed on it).
func (x indexAdapter) LatestInvalid(expID string) (string, []string, bool) {
	a, err := x.latestAnalysis(expID)
	if err != nil || a == nil || a.Verdict.Decision != schemas.DecisionInvalid {
		return "", nil, false
	}
	scope := a.Verdict.InvalidScope
	onlyGates := len(a.Verdict.InvalidReasons) > 0
	for _, r := range a.Verdict.InvalidReasons {
		onlyGates = onlyGates && verdict.IsGateRateReason(r)
	}
	if scope != "runs" || !onlyGates {
		scope = "experiment" // a reason added after the policy decided keeps the whole experiment invalid
	}
	return scope, a.Verdict.InvalidReasons, true
}

// Regrades lists an experiment's regrades from its regrades/ folder, each
// read with the store's regrade.json reader. A folder without regrade.json
// (a regrade interrupted before its first write) is skipped; any other
// unreadable regrade is an error. An experiment without the folder has none.
func (x indexAdapter) Regrades(ctx context.Context, expID string) ([]leaderboard.Regrade, error) {
	entries, err := os.ReadDir(filepath.Join(x.root, expID, regradesDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list regrades of %s: %w", expID, err)
	}
	st, err := x.readStore()
	if err != nil {
		return nil, err
	}
	var out []leaderboard.Regrade
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := st.RegradeInfo(ctx, expID, e.Name())
		if errors.Is(err, schemas.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, leaderboard.Regrade{ID: e.Name(), CreatedAt: info.CreatedAt, Note: info.Note})
	}
	return out, nil
}

// RegradedRuns reads an experiment's counted sealed runs with the regrade as
// grade source, through the store's SealedRuns, so the leaderboard reads a
// regrade exactly as analysis does, seal and regate hash checks included. A
// run whose seal does not verify comes back with its problem; the board's
// own seal check (VerifySeal) then leaves it out.
func (x indexAdapter) RegradedRuns(ctx context.Context, expID, regradeID string) ([]schemas.SealedRun, error) {
	st, err := x.readStore()
	if err != nil {
		return nil, err
	}
	return st.SealedRuns(ctx, expID, regradeID)
}

// readStore returns the index's own store when the adapter has an index, so
// a regrade read shares the seal verification cache, or else opens the run
// store at the adapter's root for reading. The root must exist, because
// opening a store creates a missing root and a read never creates anything.
func (x indexAdapter) readStore() (*store.FSStore, error) {
	if x.ix != nil && x.ix.Store() != nil {
		return x.ix.Store(), nil
	}
	if _, err := os.Stat(x.root); err != nil {
		return nil, fmt.Errorf("open run store for reading: %w", err)
	}
	st, err := store.NewFSStore(x.root, schemas.NoopLogger{})
	if err != nil {
		return nil, fmt.Errorf("open run store for reading: %w", err)
	}
	return st, nil
}
