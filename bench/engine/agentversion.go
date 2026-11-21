package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// agentVersionPath maps an agent path pinned to a version,
// agents/<id>.yaml@v<N>, to the frozen snapshot
// .heimdall/agent-versions/<id>/v<N>.yaml of the project. A built-in library
// agent the project does not hold has one version, v1: its library file.
// Any other path is returned unchanged.
func (p *Planner) agentVersionPath(cfgPath string) (string, error) {
	base, id, n, ok := schemas.ParseAgentVersionRef(cfgPath)
	if !ok {
		return cfgPath, nil
	}
	snap := schemas.AgentVersionSnapshotPath(id, n)
	if fi, err := os.Stat(filepath.Join(p.benchRoot, filepath.FromSlash(snap))); err == nil && fi.Mode().IsRegular() {
		return snap, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("agent %s version %d: %w", id, n, err)
	}
	if _, lib := p.libraryFile(base); lib {
		if n == 1 {
			return base, nil
		}
		return "", fmt.Errorf("agent %s is built in and has only version 1, not %d", id, n)
	}
	return "", fmt.Errorf("agent %s has no version %d (%s not found): GET /agents/%s/versions lists them", id, n, snap, id)
}
