package resultsindex

// Database area (P1, docs/platform-design.md): what lets the database index
// (package db) share this package's verified derivation and serve the same
// Reader. Nothing here changes the JSONL index.

import (
	"context"
)

// Source is a results index that can be brought up to date and read: the
// JSONL Index of this package, or the database index (db.OrgIndex) when a
// database is configured. trends.Build reads the Snapshot that Load returns.
type Source interface {
	Sync(ctx context.Context) (Status, error)
	Rebuild(ctx context.Context) (Status, error)
	UpdateExperiment(ctx context.Context, expID string) error
	Load(ctx context.Context) (*Snapshot, error)
}

var _ Source = (*Index)(nil)

// Load implements Source: the index file as it is now.
func (ix *Index) Load(context.Context) (*Snapshot, error) { return ix.Snapshot() }

// Derive reads one experiment through the path Rebuild uses (the manifest,
// store.SealedRuns and the analysis folder): rows of verified runs, a
// Rejected entry for every run that does not verify or an experiment that
// cannot be read. The index file is neither read nor written. The returned
// snapshot holds that experiment only, sorted, with Experiments[expID] set to
// this package's fingerprint.
func (ix *Index) Derive(ctx context.Context, expID string) (*Snapshot, error) {
	s := &Snapshot{Experiments: map[string]string{}}
	if err := ix.addExperiment(ctx, s, expID); err != nil {
		return nil, err
	}
	s.sortRows()
	return s, nil
}

// NewSnapshot is an empty snapshot for rows kept elsewhere: format names the
// store in Status ("sqlite", "postgres"), path where it lives, exists whether
// it was ever built. Fill the exported fields, then call Sort.
func NewSnapshot(format, path string, exists bool) *Snapshot {
	return &Snapshot{Experiments: map[string]string{}, path: path, exists: exists, format: format}
}

// Sort puts the row lists in the order the JSONL index keeps them.
func (s *Snapshot) Sort() { s.sortRows() }
