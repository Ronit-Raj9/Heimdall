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

// lintClockTask checks the run clock (Z17, schemas/clock.go) of a task and
// of its suite.
func lintClockTask(s *schemas.Suite, t *schemas.Task) []Problem {
	var out []Problem
	if err := t.Clock.Validate(); err != nil {
		out = append(out, Problem{Path: rel(s.Root, filepath.Join(t.Dir, TaskFile)), Severity: SeverityError, Message: err.Error()})
	}
	if err := s.Clock.Validate(); err != nil {
		out = append(out, Problem{Path: rel(s.Root, filepath.Join(s.Root, "suite.yaml")), Severity: SeverityError, Message: "suite " + err.Error()})
	}
	return out
}
