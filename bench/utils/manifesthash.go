package utils

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ManifestHash returns the sha256 of the manifest's canonical JSON with Hash
// empty: the experiment's frozen identity. engine.ManifestHash is this.
func ManifestHash(m *schemas.Manifest) (string, error) {
	if m == nil {
		return "", errors.New("manifest is nil")
	}
	c := *m
	c.Hash = ""
	h, err := HashJSON(&c)
	if err != nil {
		return "", fmt.Errorf("hash manifest %s: %w", m.ExpID, err)
	}
	return h, nil
}

// VerifyManifestHash recomputes the manifest's hash and returns an error
// when it differs from the recorded Hash, so a manifest.json whose settings
// were edited after freezing is never read as the frozen one (audit 1-A-02).
// Compatibility rule: a manifest that records no hash (written before hashes
// existed) has nothing to verify against and passes; every manifest the
// planner writes records one.
func VerifyManifestHash(m *schemas.Manifest) error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	if m.Hash == "" {
		return nil
	}
	h, err := ManifestHash(m)
	if err != nil {
		return err
	}
	if h != m.Hash {
		return fmt.Errorf("manifest %s does not match its hash: recorded %s, computed %s", m.ExpID, m.Hash, h)
	}
	return nil
}
