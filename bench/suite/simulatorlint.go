package suite

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/utils"
)

// W3 customer support (docs/customer-support.md): lint of the user
// simulator settings and of each simulated user's file.

// lintSimulatorSuite checks suite.yaml's simulator block. A provider or
// model still at the importer's placeholder is a warning (plan refuses it
// until pinned); anything else Check refuses is an error.
func lintSimulatorSuite(s *schemas.Suite) []Problem {
	if s.Simulator == nil {
		return nil
	}
	var out []Problem
	add := func(sev, format string, args ...any) {
		out = append(out, Problem{Path: SuiteFile, Severity: sev, Message: fmt.Sprintf(format, args...)})
	}
	if err := simuser.Check(simuser.Effective(s, nil)); err != nil {
		sev := SeverityError
		if strings.Contains(err.Error(), simuser.Placeholder) {
			sev = SeverityWarning
		}
		add(sev, "simulator: %v", err)
	}
	backed := len(simuser.SimulatedServices(s, nil)) > 0
	for _, t := range s.Tasks {
		backed = backed || len(simuser.SimulatedServices(nil, t)) > 0
	}
	if !backed && len(s.Tasks) > 0 {
		add(SeverityWarning, "simulator: set, but no service plays the user (role user, or gateway: {})")
	}
	return out
}

// lintSimulatorTask checks the user file of each simulated user of a task:
// it must parse and drive the scripted user (admission uses it); a missing
// persona is a warning, since llm mode cannot play that user.
func lintSimulatorTask(t *schemas.Task, taskYAML string) []Problem {
	var out []Problem
	for _, sv := range simuser.SimulatedServices(nil, t) {
		rel, ok := sv.Files[simuser.UserFile]
		if !ok {
			continue
		}
		p, err := utils.ResolveInside(t.Dir, rel)
		if err != nil {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("services: %s user file: %v", sv.ID, err)})
			continue
		}
		if err := simuser.CheckUserFile(p, schemas.SimulatorScripted); err != nil {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityError, Message: fmt.Sprintf("services: %s: %v", sv.ID, err)})
			continue
		}
		if err := simuser.CheckUserFile(p, schemas.SimulatorLLM); err != nil {
			out = append(out, Problem{Path: taskYAML, Severity: SeverityWarning, Message: fmt.Sprintf("services: %s: %v", sv.ID, err)})
		}
	}
	return out
}
