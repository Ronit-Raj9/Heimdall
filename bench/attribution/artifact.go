package attribution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// BlameDir is the experiment subfolder that holds blame artifacts:
// <store root>/<exp_id>/blame/<run_id>/blame.json. Like a regrade or a
// review, a blame is a child artifact outside the sealed run; it names the
// run's seal hash (ParentSeal) and never touches the run folder.
const BlameDir = "blame"

var idPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// Path returns where the blame of runID in expID lives under root. Both ids
// must be store ids ([a-z0-9_]+), so no id can steer the path outside the
// experiment folder.
func Path(root, expID, runID string) (string, error) {
	if !idPattern.MatchString(expID) || len(expID) > 128 {
		return "", fmt.Errorf("blame path: invalid exp id %q", expID)
	}
	if !idPattern.MatchString(runID) || len(runID) > 128 {
		return "", fmt.Errorf("blame path: invalid run id %q", runID)
	}
	return filepath.Join(root, expID, BlameDir, runID, BlameFile), nil
}

// Write stores b as the blame of its run, replacing an earlier one (a later
// blame of the same run, for example with a replay, supersedes it). The
// write is atomic: a temp file in the same folder, fsynced, then renamed.
func Write(root string, b *Blame) error {
	if b == nil {
		return errors.New("write blame: nil blame")
	}
	p, err := Path(root, b.ExpID, b.RunID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("encode blame of run %s: %w", b.RunID, err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("create blame folder of run %s: %w", b.RunID, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".blame-*")
	if err != nil {
		return fmt.Errorf("write blame of run %s: %w", b.RunID, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write blame of run %s: %w", b.RunID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync blame of run %s: %w", b.RunID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close blame of run %s: %w", b.RunID, err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("place blame of run %s: %w", b.RunID, err)
	}
	return nil
}

// Read loads the blame of runID in expID. It returns nil and no error when
// none was written. A blame whose ParentSeal differs from sealHash (when
// both are known) describes another sealing of the run and is refused.
func Read(root, expID, runID, sealHash string) (*Blame, error) {
	p, err := Path(root, expID, runID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read blame of run %s: %w", runID, err)
	}
	var b Blame
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("decode blame of run %s: %w", runID, err)
	}
	if sealHash != "" && b.ParentSeal != "" && strings.TrimPrefix(b.ParentSeal, "sha256:") != strings.TrimPrefix(sealHash, "sha256:") {
		return nil, fmt.Errorf("blame of run %s names seal %s, the run is sealed as %s", runID, short(b.ParentSeal), short(sealHash))
	}
	return &b, nil
}

// ContentHash is the sha256 of b's canonical JSON, for a caller that keeps a
// log of blames.
func ContentHash(b *Blame) (string, error) {
	return utils.HashJSON(b)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
