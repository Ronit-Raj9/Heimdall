package tenancy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// ImportedExperiment is one experiment folder of an import.
type ImportedExperiment struct {
	ExpID  string `json:"exp_id"`
	Runs   int    `json:"runs"`   // sealed runs verified
	Status string `json:"status"` // imported | skipped (already present) | refused
	Reason string `json:"reason,omitempty"`
}

// ImportReport is what ImportRuns did.
type ImportReport struct {
	Experiments []ImportedExperiment `json:"experiments"`
	Imported    int                  `json:"imported"`
	Skipped     int                  `json:"skipped"`
	Refused     int                  `json:"refused"`
	Runs        int                  `json:"runs"`
}

var expIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

// importStaging is the staging folder name prefix inside the destination
// store root; a leading dot keeps it out of the store's experiment list.
const importStaging = ".import-"

// ImportRuns copies the sealed experiment folders of src (a runs folder:
// one folder per experiment with its manifest.json) into the store rooted
// at dstRoot. Each experiment is copied to a staging folder inside dstRoot
// first, then every sealed run's seal and the experiment's seals.log chain
// are verified on the copy with the store's own verification, and only then
// is it renamed into place. An experiment that does not verify is refused
// and nothing of it is kept; one already present is skipped untouched. src
// is only read.
func ImportRuns(ctx context.Context, src, dstRoot string, logger schemas.Logger) (*ImportReport, error) {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	dstAbs, err := filepath.Abs(dstRoot)
	if err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	if srcAbs == dstAbs || within(srcAbs, dstAbs) || within(dstAbs, srcAbs) {
		return nil, fmt.Errorf("import: source %s and destination %s overlap", srcAbs, dstAbs)
	}
	entries, err := os.ReadDir(srcAbs)
	if err != nil {
		return nil, fmt.Errorf("import: read %s: %w", srcAbs, err)
	}
	if err := os.MkdirAll(dstAbs, 0o755); err != nil {
		return nil, fmt.Errorf("import: %w", err)
	}
	var ids []string
	for _, e := range entries {
		if !e.IsDir() || !expIDPattern.MatchString(e.Name()) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(srcAbs, e.Name(), "manifest.json")); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		ids = append(ids, e.Name())
	}
	sort.Strings(ids)
	rep := &ImportReport{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		got := importOne(ctx, srcAbs, dstAbs, id, logger)
		rep.Experiments = append(rep.Experiments, got)
		switch got.Status {
		case "imported":
			rep.Imported++
			rep.Runs += got.Runs
		case "skipped":
			rep.Skipped++
		default:
			rep.Refused++
		}
	}
	if rep.Imported > 0 {
		// A server process other than this one may cache the org's index:
		// the new generation makes its registry rebuild the runtime.
		if err := BumpGeneration(dstAbs); err != nil {
			return rep, fmt.Errorf("import: %w", err)
		}
	}
	return rep, nil
}

func importOne(ctx context.Context, src, dst, id string, logger schemas.Logger) ImportedExperiment {
	out := ImportedExperiment{ExpID: id}
	final := filepath.Join(dst, id)
	if _, err := os.Lstat(final); err == nil {
		out.Status, out.Reason = "skipped", "already present in the organization"
		return out
	}
	staging, err := os.MkdirTemp(dst, importStaging)
	if err != nil {
		out.Status, out.Reason = "refused", err.Error()
		return out
	}
	defer os.RemoveAll(staging)
	if err := CopyTree(filepath.Join(src, id), filepath.Join(staging, id)); err != nil {
		out.Status, out.Reason = "refused", err.Error()
		return out
	}
	n, err := VerifyExperiment(ctx, staging, id, logger)
	if err != nil {
		out.Status, out.Reason = "refused", err.Error()
		return out
	}
	if err := os.Rename(filepath.Join(staging, id), final); err != nil {
		out.Status, out.Reason = "refused", err.Error()
		return out
	}
	out.Status, out.Runs = "imported", n
	return out
}

// VerifyExperiment verifies every sealed run of expID in the store rooted at
// root against its seal and seals.log, and the seals.log chain, returning
// the number of sealed runs. An experiment with no sealed run is refused:
// there is nothing to serve as evidence.
func VerifyExperiment(ctx context.Context, root, expID string, logger schemas.Logger) (int, error) {
	st, err := store.NewFSStore(root, logger)
	if err != nil {
		return 0, err
	}
	if _, err := st.LoadManifest(ctx, expID); err != nil {
		return 0, fmt.Errorf("manifest: %w", err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		return 0, err
	}
	runs, ok := ix.Runs(expID)
	if !ok {
		return 0, fmt.Errorf("experiment %s does not index", expID)
	}
	n := 0
	for _, r := range runs {
		if !r.Sealed {
			continue
		}
		if err := st.Verify(ctx, expID, r.Run.RunID); err != nil {
			return n, fmt.Errorf("run %s: %w", r.Run.RunID, err)
		}
		n++
	}
	if err := st.VerifyChain(expID); err != nil {
		return n, err
	}
	if n == 0 {
		return 0, errors.New("no sealed run to import")
	}
	return n, nil
}

// within reports whether path is inside root (or root itself).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) && (rel == "." || !startsDotDot(rel))
}

func startsDotDot(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && os.IsPathSeparator(rel[2])
}
