package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading (docs/visual-grading.md): the images a visual grade
// wrote into the run folder.

// visualDir is the run folder's folder of visual grade images.
const visualDir = "visual"

// visualNames are the only files served from it.
var visualNames = map[string]bool{"capture.png": true, "reference.png": true, "diff.png": true}

// RunVisual returns the raw bytes of visual/<name> in a run folder, for name
// capture.png, reference.png or diff.png only. The folder must be a real
// folder and the file a regular file: a symlink, any other name, a missing
// file or an unknown run is schemas.ErrNotFound. It does not check the file
// against the run's seal; callers that serve it do.
func (ix *Index) RunVisual(runID, name string) ([]byte, error) {
	if !visualNames[name] {
		return nil, fmt.Errorf("visual file %q: not served: %w", name, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	dir := filepath.Join(r.Dir, visualDir)
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("run %s has no visual folder: %w", runID, schemas.ErrNotFound)
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s visual file %s: %w", runID, name, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("run %s visual file %s: %w", runID, name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s visual file %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	return os.ReadFile(p)
}
