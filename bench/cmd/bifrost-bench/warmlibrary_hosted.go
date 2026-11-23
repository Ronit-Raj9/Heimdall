package main

// Fix wave 3 (L1): warm the built-in library's suite summaries at hosted
// start, so the first suite list, estimate or comparison page after a
// restart does not wait on a cold pack.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/suite"
)

// warmSuitesEnv turns the warm off with "off"; unset or anything else
// leaves it on.
const warmSuitesEnv = "HEIMDALL_WARM_SUITES"

const (
	// warmMax is the most library packs warmed at start.
	warmMax = 15
	// warmLargest is how many of the largest library packs (by task
	// folders) follow the recently used ones.
	warmLargest = 5
	// warmRecent is how far back "recently used" looks.
	warmRecent = 14 * 24 * time.Hour
)

// warmLibraryDelay lets the listener come up before the warm starts
// reading packs; tests set it to zero.
var warmLibraryDelay = 2 * time.Second

// warmRun is handlers.WarmSuites; tests replace it.
var warmRun = handlers.WarmSuites

// warmLibrary warms the full summaries (and with them the full loads) of
// the library packs worth having ready: the ones any org used in the last
// 14 days, newest first, then the largest, at most warmMax. It runs beside
// the listener (serveHosted starts it with go after the listen line), one
// pack at a time, and returns when the warm ends or ctx does. Library
// entries are keyed by folder, so every org's suite list then hits them.
func warmLibrary(ctx context.Context, a *app, env *hostedEnv) {
	if strings.EqualFold(strings.TrimSpace(os.Getenv(warmSuitesEnv)), "off") {
		a.logger.Info("suite warm skipped (%s=off)", warmSuitesEnv)
		return
	}
	lib := suite.LibraryBenchmarks(a.library)
	if lib == "" {
		return
	}
	if warmLibraryDelay > 0 {
		t := time.NewTimer(warmLibraryDelay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
	ids, err := suiteIDsIn(lib)
	if err != nil {
		a.logger.Warn("suite warm: %v", err)
		return
	}
	sizes := make(map[string]int, len(ids))
	for _, id := range ids {
		if entries, err := os.ReadDir(filepath.Join(lib, id, "tasks")); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					sizes[id]++
				}
			}
		}
	}
	var recent []string
	if env != nil && env.db != nil {
		if recent, err = recentSuites(ctx, env.db, time.Now().Add(-warmRecent)); err != nil {
			a.logger.Warn("suite warm: recently used benchmarks: %v", err)
		}
	}
	pick := warmPick(recent, ids, sizes)
	if len(pick) == 0 {
		return
	}
	src := serveSuites{root: filepath.Join(a.library, ".no-org"), cfg: a.cfg, reg: a.reg, library: lib}
	h := handlers.NewSuitesHandler(a.cfg.Server.Mode, src).WithHeldOutVisible(a.cfg.HeldOut.Visible)
	<-warmRun(ctx, h, pick, a.logger)
}

// recentSuites lists the suite references (id@version) experiments of any
// org were created on since since, newest first.
func recentSuites(ctx context.Context, d *db.DB, since time.Time) ([]string, error) {
	rows, err := d.Query(ctx, `SELECT suite, MAX(created_at) AS last FROM idx_experiments WHERE created_at > ? GROUP BY suite ORDER BY last DESC`, db.Millis(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ref string
		var last int64
		if err := rows.Scan(&ref, &last); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// warmPick is the warm order: the library packs among recent (suite
// references, newest first; the version is dropped and org packs left
// out), then the warmLargest library packs with the most task folders not
// already picked, at most warmMax in all.
func warmPick(recent, library []string, sizes map[string]int) []string {
	inLibrary := make(map[string]bool, len(library))
	for _, id := range library {
		inLibrary[id] = true
	}
	picked := map[string]bool{}
	var out []string
	add := func(id string) {
		if len(out) < warmMax && inLibrary[id] && !picked[id] {
			picked[id] = true
			out = append(out, id)
		}
	}
	for _, ref := range recent {
		id, _, _ := strings.Cut(ref, "@")
		add(id)
	}
	largest := append([]string(nil), library...)
	sort.SliceStable(largest, func(i, j int) bool {
		if sizes[largest[i]] != sizes[largest[j]] {
			return sizes[largest[i]] > sizes[largest[j]]
		}
		return largest[i] < largest[j]
	})
	n := 0
	for _, id := range largest {
		if n == warmLargest {
			break
		}
		if !picked[id] {
			add(id)
			n++
		}
	}
	return out
}
