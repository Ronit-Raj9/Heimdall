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

var (
	recordingServiceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)
	recordingFileRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// RunRecordingPath returns the host path of a run's recording file (Z03,
// schemas/recordings.go): recordings/<service>/<name>, or the index
// recordings/recordings.jsonl when service is empty and name is the index
// name. The folders must be real folders and the file a regular file: a
// symlink, a name with a path separator, a missing file or an unknown run
// is schemas.ErrNotFound. The caller opens the file itself so a video can
// be served in ranges without reading it whole.
func (ix *Index) RunRecordingPath(runID, service, name string) (string, error) {
	switch {
	case service == "" && name == schemas.RecordingsIndexFile:
	case recordingServiceRE.MatchString(service) && recordingFileRE.MatchString(name) && name != schemas.RecordingsIndexFile:
	default:
		return "", fmt.Errorf("recording %q of %q: not served: %w", name, service, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return "", fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	dir := filepath.Join(r.Dir, schemas.RecordingsDir)
	if service != "" {
		if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
			return "", fmt.Errorf("run %s has no recordings: %w", runID, schemas.ErrNotFound)
		}
		dir = filepath.Join(dir, service)
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("run %s has no recordings folder %s: %w", runID, service, schemas.ErrNotFound)
	}
	p := filepath.Join(dir, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("run %s recording %s: %w", runID, name, schemas.ErrNotFound)
		}
		return "", fmt.Errorf("run %s recording %s: %w", runID, name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("run %s recording %s: not a regular file: %w", runID, name, schemas.ErrNotFound)
	}
	return p, nil
}
