package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

const leaderboardUsage = `usage: bifrost-bench leaderboard [-config deploy/bench.yaml] [-suite id@version] [-condition clean] [-scope all|common] [-synthetic exclude|only] [-format md|json] [-out file]
Ranks agent configurations by verified success over every sealed run of one suite version under one condition, across experiments. Reads the run folder only; no gateway is needed.`

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
	format := fs.String("format", "md", "md or json")
	out := fs.String("out", "", "write to this file instead of stdout")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, leaderboardUsage)
		return exitUsage
	}
	if *format != "md" && *format != "json" {
		fmt.Fprintf(os.Stderr, "format %q is not md or json\n%s\n", *format, leaderboardUsage)
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
	lb, err := leaderboard.Build(ctx, src, leaderboard.Query{
		Suite: *suiteFlag, Condition: *condition, Scope: *scope, Synthetic: *synthetic,
	})
	if errors.Is(err, leaderboard.ErrBadQuery) {
		fmt.Fprintf(os.Stderr, "%v\n%s\n", err, leaderboardUsage)
		return exitUsage
	}
	if err != nil {
		return fail("leaderboard: %v", err)
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

// LatestDecision returns the verdict decision of the experiment's newest
// analysis (by LatestAnalysis) from its verdict.json, so the leaderboard can
// leave out experiments whose verdict is invalid. An unreadable verdict reads
// as no decision: the experiment's runs then stay on the board.
func (x indexAdapter) LatestDecision(expID string) (schemas.Decision, bool) {
	id, ok := x.LatestAnalysis(expID)
	if !ok {
		return "", false
	}
	return readDecision(filepath.Join(x.root, expID, "analysis", id, "verdict.json"))
}

// readDecision reads the verdict decision from an analysis verdict.json.
func readDecision(path string) (schemas.Decision, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var a struct {
		Verdict struct {
			Decision schemas.Decision `json:"decision"`
		} `json:"verdict"`
	}
	if err := json.Unmarshal(data, &a); err != nil || a.Verdict.Decision == "" {
		return "", false
	}
	return a.Verdict.Decision, true
}
