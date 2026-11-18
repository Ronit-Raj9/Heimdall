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

// X8 voice agents (docs/voice-agents.md): the voice caller's exports of a
// run, state/<caller>/<name>.

// voiceNames are the only caller exports served.
var voiceNames = map[string]bool{"timeline.jsonl": true, "caller.wav": true, "agent.wav": true}

var voiceServiceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// RunVoiceFile returns the raw bytes of state/<service>/<name> in a run
// folder for the caller's timeline and audio tracks only. A symlink, another
// name, a missing file or an unknown run is schemas.ErrNotFound. It does not
// check the file against the run's evidence or seal; callers that serve it do.
func (ix *Index) RunVoiceFile(runID, service, name string) ([]byte, error) {
	if !voiceNames[name] || !voiceServiceRe.MatchString(service) {
		return nil, fmt.Errorf("voice file %s/%s: not served: %w", service, name, schemas.ErrNotFound)
	}
	r, ok := ix.snap.Load().byRun[runID]
	if !ok {
		return nil, fmt.Errorf("run %s: %w", runID, schemas.ErrNotFound)
	}
	for _, d := range []string{filepath.Join(r.Dir, "state"), filepath.Join(r.Dir, "state", service)} {
		if info, err := os.Lstat(d); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("run %s has no %s: %w", runID, d, schemas.ErrNotFound)
		}
	}
	p := filepath.Join(r.Dir, "state", service, name)
	info, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("run %s voice file %s/%s: %w", runID, service, name, schemas.ErrNotFound)
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("run %s voice file %s/%s: not a regular file: %w", runID, service, name, schemas.ErrNotFound)
	}
	return os.ReadFile(p)
}
