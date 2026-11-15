package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// RunArtifact returns the raw bytes of one artifact file of a run folder,
// artifacts/<id>/<name> (utils.ArtifactRunPath). The id and name must pass
// the artifact naming rules before anything on disk is touched, so a lookup
// can never leave the run's artifacts folder. Every step of the path must be
// a real folder and the file a regular file: a symlink anywhere is
// schemas.ErrNotFound, as is a missing file or an unknown run. It does not
// check the file against the run's seal; callers that serve it do.
func (ix *Index) RunArtifact(runID, id, name string) ([]byte, error) {
	if utils.CheckArtifactSpec(schemas.ArtifactSpec{ID: id, Path: name}) != nil || !utils.ArtifactNameOK(name) {
		return nil, fmt.Errorf("artifact %q of %q: not served: %w", name, id, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	dir := r.Dir
	for _, step := range []string{utils.ArtifactsDir, id} {
		dir = filepath.Join(dir, step)
		info, err := os.Lstat(dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("run %s artifact %s/%s: %w", runID, id, name, schemas.ErrNotFound)
			}
			return nil, fmt.Errorf("run %s artifact %s/%s: %w", runID, id, name, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("run %s artifact %s/%s: %s is not a folder: %w", runID, id, name, step, schemas.ErrNotFound)
		}
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s artifact %s/%s: %w", runID, id, name, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("run %s artifact %s/%s: %w", runID, id, name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s artifact %s/%s: not a regular file: %w", runID, id, name, schemas.ErrNotFound)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("run %s artifact %s/%s: %w", runID, id, name, err)
	}
	return b, nil
}
