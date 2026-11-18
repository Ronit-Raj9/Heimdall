package codescan

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// LoadRun reads the evidence the code scan needs from a run folder:
// evidence.json (its diff record and any code scan) with the diff pointed at
// the folder's workspace.diff, whose sha256 must match the recorded one. A
// folder with no evidence.json is read from workspace.diff alone. The caller
// verifies the seal first; this only reads.
func LoadRun(dir string) (*schemas.Evidence, error) {
	ev := &schemas.Evidence{}
	b, err := os.ReadFile(filepath.Join(dir, "evidence.json"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("read evidence: %w", err)
	default:
		if err := json.Unmarshal(b, ev); err != nil {
			return nil, fmt.Errorf("decode evidence: %w", err)
		}
	}
	p := filepath.Join(dir, "workspace.diff")
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			ev.Diff = nil
			return ev, nil
		}
		return nil, err
	}
	if ev.Diff == nil {
		ev.Diff = &schemas.Diff{}
	}
	ev.Diff.Path = p
	if want := ev.Diff.SHA256; want != "" {
		sum, err := utils.SHA256File(p)
		if err != nil {
			return nil, fmt.Errorf("hash diff: %w", err)
		}
		if !sameDigest(sum, want) {
			return nil, fmt.Errorf("workspace.diff sha256 %s, evidence records %s: %w", sum, want, schemas.ErrSealMismatch)
		}
	}
	return ev, nil
}

// sameDigest compares two sha256 digests with or without the "sha256:"
// prefix.
func sameDigest(a, b string) bool {
	strip := func(s string) string {
		if len(s) > 7 && s[:7] == "sha256:" {
			return s[7:]
		}
		return s
	}
	return strip(a) == strip(b)
}
