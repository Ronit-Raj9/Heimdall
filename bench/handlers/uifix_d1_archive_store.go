package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Archive sidecar (UI fix wave D1, decision 6). Comparisons and runs are
// sealed and must never be rewritten, and an improvement run's own record is
// written by its runner, so their archive marks live in one file of the
// organization's project: <root>/.heimdall/archive.json, mapping each id to
// when and by whom it was archived. Evaluations and findings carry the mark
// in their own records (evalstore, findings). Nothing here changes a seal,
// a hash, a score or a verdict: only lists read the marks.

// Archive kinds of the sidecar.
const (
	archiveExperiments  = "experiments"
	archiveRuns         = "runs"
	archiveImprovements = "improvements"
)

// archiveFile is the sidecar of one project root.
type archiveFile struct {
	Experiments  map[string]schemas.Archived `json:"experiments,omitempty"`
	Runs         map[string]schemas.Archived `json:"runs,omitempty"`
	Improvements map[string]schemas.Archived `json:"improvements,omitempty"`
}

func (a *archiveFile) kind(kind string) map[string]schemas.Archived {
	switch kind {
	case archiveExperiments:
		if a.Experiments == nil {
			a.Experiments = map[string]schemas.Archived{}
		}
		return a.Experiments
	case archiveRuns:
		if a.Runs == nil {
			a.Runs = map[string]schemas.Archived{}
		}
		return a.Runs
	case archiveImprovements:
		if a.Improvements == nil {
			a.Improvements = map[string]schemas.Archived{}
		}
		return a.Improvements
	}
	panic("unknown archive kind " + kind)
}

// mark is the archive mark of id, empty when it is not archived.
func (a archiveFile) mark(kind, id string) schemas.Archived {
	return a.kind(kind)[id]
}

// archiveMaxBytes bounds the sidecar read: 4 MiB holds tens of thousands
// of marks.
const archiveMaxBytes = 4 << 20

var archiveLocks sync.Map // path -> *sync.Mutex

func archivePath(root string) string { return filepath.Join(root, ".heimdall", "archive.json") }

// readArchive reads the sidecar of root. No root or no file is an empty
// sidecar.
func readArchive(root string) (archiveFile, error) {
	var a archiveFile
	if root == "" {
		return a, nil
	}
	f, err := os.Open(archivePath(root))
	if errors.Is(err, os.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return a, err
	}
	if st.Size() > archiveMaxBytes {
		return a, fmt.Errorf("archive file is larger than %d bytes", archiveMaxBytes)
	}
	if err := json.NewDecoder(f).Decode(&a); err != nil {
		return archiveFile{}, fmt.Errorf("read archive file: %w", err)
	}
	return a, nil
}

// setArchive archives (archive true) or unarchives id of kind in root's
// sidecar, atomically under a lock, and returns the mark now stored.
// Archiving an archived id keeps its first mark.
func setArchive(root, kind, id string, archive bool, by string, at time.Time) (schemas.Archived, error) {
	if root == "" {
		return schemas.Archived{}, errors.New("no project folder")
	}
	p := archivePath(root)
	mu, _ := archiveLocks.LoadOrStore(p, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	a, err := readArchive(root)
	if err != nil {
		return schemas.Archived{}, err
	}
	m := a.kind(kind)
	switch {
	case !archive:
		if _, ok := m[id]; !ok {
			return schemas.Archived{}, nil
		}
		delete(m, id)
	case m[id].IsArchived():
		return m[id], nil
	default:
		t := at.UTC()
		m[id] = schemas.Archived{ArchivedAt: &t, ArchivedBy: by}
	}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return schemas.Archived{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return schemas.Archived{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".archive-*")
	if err != nil {
		return schemas.Archived{}, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return schemas.Archived{}, err
	}
	if err := tmp.Close(); err != nil {
		return schemas.Archived{}, err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return schemas.Archived{}, err
	}
	return m[id], nil
}

// dropArchived removes the marks of an erased comparison and its runs, so
// a later object with the same id does not start archived. Errors are
// ignored: a stale mark only hides nothing that exists.
func dropArchived(root, expID string, runIDs []string) {
	_, _ = setArchive(root, archiveExperiments, expID, false, "", time.Time{})
	for _, id := range runIDs {
		_, _ = setArchive(root, archiveRuns, id, false, "", time.Time{})
	}
}

// archiveFilter is the archived= query of the lists: hide archived items
// (default), include them, or keep only them.
type archiveFilter int

const (
	archiveHide archiveFilter = iota
	archiveInclude
	archiveOnly
)

// ArchivedQueryDoc documents archived= on every list that takes it.
var ArchivedQueryDoc = QueryDoc{Name: "archived", Description: "Archived items are left out by default; include returns all, only returns only the archived ones. Each archived row carries archived_at and archived_by; total and paging count the rows the filter keeps. Archiving changes no computed number: leaderboards, verdicts, usage and spend still count archived items"}

// readArchiveFilter reads archived= or answers a 400.
func readArchiveFilter(ctx *fasthttp.RequestCtx, mode schemas.Mode) (archiveFilter, bool) {
	switch string(ctx.QueryArgs().Peek("archived")) {
	case "", "exclude":
		return archiveHide, true
	case "include":
		return archiveInclude, true
	case "only":
		return archiveOnly, true
	}
	lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "archived must be include or only")
	return archiveHide, false
}

// keep reports whether a row with this archive state stays in the list.
func (f archiveFilter) keep(archived bool) bool {
	switch f {
	case archiveInclude:
		return true
	case archiveOnly:
		return archived
	}
	return !archived
}
