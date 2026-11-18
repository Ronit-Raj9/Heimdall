package suite

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Multi-container tasks (X2, docs/multi-container.md).

// composeConventions are the variable and workspace conventions a suite's
// compose files follow: Harbor's for a suite the Harbor importer wrote.
func composeConventions(s *schemas.Suite) string {
	if s != nil && s.Imported != nil && s.Imported.Importer == "harbor" {
		return compose.ConventionsHarbor
	}
	return compose.ConventionsCompose
}

// expandCompose maps a task's compose file onto its environment and
// services, before anything else reads the task. Every refused setting is a
// load error naming its place in the file, so plan, run and lint all refuse
// the task with the reason; changed and ignored settings are lint warnings
// (lintCompose). Compose services start before the task.yaml services.
func expandCompose(s *schemas.Suite, t *schemas.Task, d displayer) error {
	if t == nil || t.Compose == "" {
		return nil
	}
	file := d.name(filepath.Join(t.Dir, TaskFile))
	res, err := compose.Expand(t.Dir, t.Compose, t.Environment, compose.Options{Conventions: composeConventions(s)})
	if err != nil {
		return fmt.Errorf("%s: compose: %w", file, err)
	}
	if refused := res.Refused(); len(refused) > 0 {
		errs := make([]error, 0, len(refused))
		for _, f := range refused {
			errs = append(errs, fmt.Errorf("%s: compose %s: %s: refused: %s", file, t.Compose, f.Where, f.Message))
		}
		return errors.Join(errs...)
	}
	t.Environment = res.Environment
	t.Services = append(res.Services, t.Services...)
	return nil
}

// lintCompose reports what the compose mapping changed or ignored, one
// warning per setting, so the task author sees how the file runs here.
func lintCompose(s *schemas.Suite, t *schemas.Task, taskYAML string) []Problem {
	if t.Compose == "" {
		return nil
	}
	// The loaded environment already holds the mapping, so the findings come
	// from task.yaml's own environment, read again.
	var orig schemas.Task
	path := filepath.Join(t.Dir, TaskFile)
	if err := DecodeYAMLFile(path, taskYAML, SchemaTask, &orig); err != nil {
		return []Problem{{Path: taskYAML, Severity: SeverityError, Message: "compose: " + err.Error()}}
	}
	res, err := compose.Expand(t.Dir, t.Compose, orig.Environment, compose.Options{Conventions: composeConventions(s)})
	if err != nil {
		return []Problem{{Path: taskYAML, Severity: SeverityError, Message: "compose: " + err.Error()}}
	}
	var out []Problem
	for _, f := range res.Findings {
		sev := SeverityWarning
		if f.Level == compose.LevelRefused {
			sev = SeverityError
		}
		out = append(out, Problem{Path: rel(s.Root, filepath.Join(t.Dir, filepath.FromSlash(t.Compose))), Severity: sev,
			Message: f.Where + ": " + f.Level + ": " + f.Message})
	}
	return out
}

// composeImages are the image tags of a compose task's services, by role
// service/<id>, for the task's lock fingerprint: a pulled image binds by its
// registry digest, a local build by tag, as task images do. Tasks without a
// compose file add none, so their fingerprints are unchanged.
func composeImages(s *schemas.Suite, t *schemas.Task) (map[string]string, error) {
	if t.Compose == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, sv := range t.Services {
		if sv.Compose == nil {
			continue
		}
		tag := sv.Image
		if sv.Build != nil {
			img, err := utils.BuildImageSpec(t.Dir, sv.Build.Dockerfile, sv.Build.Context, "svc-"+s.ID+"-"+sv.ID, t.Environment != nil && t.Environment.BuildNetwork)
			if err != nil {
				return nil, fmt.Errorf("service %s image: %w", sv.ID, err)
			}
			tag = img.Tag
		}
		if tag != "" {
			out["service/"+sv.ID] = tag
		}
	}
	return out, nil
}

// composeVerifierNetwork is the admission check of a compose task whose
// verifier queries services: it joins the run's service networks, which the
// environment creates internal (no route out), and the grader refuses any
// network that is not internal before every grade, the admission controls
// included.
func composeVerifierNetwork(services []string) schemas.AdmissionCheck {
	return schemas.AdmissionCheck{Name: "verifier_network_internal", Want: "internal", Got: "internal", Passed: true,
		Detail: "the verifier joins only the run's internal service network to query " + strings.Join(services, ", ") +
			"; the grader checks the network is internal before each grade and has no route out"}
}
