package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

var frameFileRE = regexp.MustCompile(`^[0-9]{4,6}\.(png|jpg|jpeg)$`)

// RunFrame returns the raw bytes of frames/<name> in a run folder, for the
// frame index (schemas.FramesIndexFile) or a frame image name (4 to 6 digits
// and .png, .jpg or .jpeg) only. The folder must be a real folder and the
// file a regular file: a symlink, any other name, a missing file or an
// unknown run is schemas.ErrNotFound.
func (ix *Index) RunFrame(runID, name string) ([]byte, error) {
	if name != schemas.FramesIndexFile && !frameFileRE.MatchString(name) {
		return nil, fmt.Errorf("frame file %q: not served: %w", name, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	dir := filepath.Join(r.Dir, schemas.FramesDir)
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("run %s has no frames folder: %w", runID, schemas.ErrNotFound)
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s frame %s: %w", runID, name, schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("run %s frame %s: %w", runID, name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s frame %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	return os.ReadFile(p)
}
