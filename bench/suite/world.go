package suite

import (
	"fmt"
	"path/filepath"

	"github.com/maximhq/bifrost/bench/schemas"
)

// lintWorldTask checks a task's world clock (schemas/stream.go) against its
// sessions and the services of the task and its suite.
func lintWorldTask(s *schemas.Suite, t *schemas.Task) []Problem {
	if t.World == nil {
		return nil
	}
	services := map[string]bool{}
	for _, sv := range s.Services {
		services[sv.ID] = true
	}
	for _, sv := range t.Services {
		services[sv.ID] = true
	}
	if err := t.World.Validate(len(t.Sessions), services); err != nil {
		return []Problem{{Path: rel(s.Root, filepath.Join(t.Dir, TaskFile)), Severity: SeverityError, Message: fmt.Sprintf("world: %v", err)}}
	}
	return nil
}
