package main

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/resultsindex"
)

// Gap G13: runs added outside a job (POST /uploads/runs) must show in
// Experiments, Leaderboard and Trends at once. Reindex rebuilds the org's
// in-memory experiment index and syncs the results index the same way the
// CLI `org import` does (db.SyncOrg: OrgIndex.Sync over the org's store).
func init() {
	serviceHooks = append(serviceHooks, func(a *app, s *handlers.Services) {
		s.Reindex = orgReindex(s.Index, a.results)
	})
}

// orgReindex returns the Reindex service over an org's read index and its
// results source (the database index hosted, nil locally: the JSONL index
// syncs itself on read).
func orgReindex(ix handlers.ReadIndex, results resultsindex.Source) func(context.Context) error {
	return func(ctx context.Context) error {
		if x, ok := ix.(indexAdapter); ok && x.ix != nil {
			if err := x.ix.Rebuild(ctx); err != nil {
				return fmt.Errorf("rebuild experiment index: %w", err)
			}
		}
		if results != nil {
			if _, err := results.Sync(ctx); err != nil {
				return fmt.Errorf("sync results index: %w", err)
			}
		}
		return nil
	}
}
