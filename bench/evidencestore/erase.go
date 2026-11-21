package evidencestore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Erasure (lane X16, audit N10): deleting a comparison or a whole
// organization removes its local folders and every mirrored object, so the
// mirror never brings deleted evidence back.

// EraseExperiment removes every mirrored object of one experiment and
// returns how many it removed.
func (m *Mirror) EraseExperiment(ctx context.Context, orgID, expID string) (int, error) {
	if err := validID("org id", orgID); err != nil {
		return 0, err
	}
	if err := validID("experiment id", expID); err != nil {
		return 0, err
	}
	return m.erasePrefix(ctx, expPrefix(orgID, expID))
}

// EraseOrg removes every mirrored object of an organization.
func (m *Mirror) EraseOrg(ctx context.Context, orgID string) (int, error) {
	if err := validID("org id", orgID); err != nil {
		return 0, err
	}
	return m.erasePrefix(ctx, orgID+"/")
}

func (m *Mirror) erasePrefix(ctx context.Context, prefix string) (int, error) {
	objs, err := m.Blob.List(ctx, prefix)
	if err != nil {
		return 0, fmt.Errorf("list %s: %w", prefix, err)
	}
	n := 0
	for _, o := range objs {
		if err := m.Blob.Delete(ctx, o.Key); err != nil {
			return n, fmt.Errorf("delete %s: %w", o.Key, err)
		}
		n++
	}
	return n, nil
}

// Mirror returns the service's mirror.
func (s *Service) Mirror() *Mirror { return s.mirror }

// RemoveFolder deletes a local folder that may hold read-only sealed runs
// (sealed run folders are made read-only). A missing folder is not an
// error.
func RemoveFolder(p string) error {
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(q, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(p); err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}
