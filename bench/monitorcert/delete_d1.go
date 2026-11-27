package monitorcert

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Delete removes one certificate folder (UI fix wave D1, M7-23). The id
// must be a certificate id (mc_ and letters or digits), so it can never
// name a path outside the root; a missing certificate, or one that is not a
// plain folder, is schemas.ErrNotFound. A certificate points at sealed
// evidence by hashes only, so deleting it changes no run or comparison.
func (s *CertStore) Delete(ctx context.Context, certID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !certIDRE.MatchString(certID) {
		return fmt.Errorf("monitor certificate %q: %w", certID, schemas.ErrNotFound)
	}
	dir := filepath.Join(s.root, certID)
	st, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !st.IsDir()) {
		return fmt.Errorf("monitor certificate %s: %w", certID, schemas.ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("monitor certificate %s: %w", certID, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("delete monitor certificate %s: %w", certID, err)
	}
	return nil
}
