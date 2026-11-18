package harbor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/compose"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Compose tasks (X2, docs/multi-container.md). A Harbor task may add
// services beside its main container with environment/docker-compose.yaml,
// which Harbor merges over its own base file (where "main" is the agent's
// container, built from environment/Dockerfile). The importer maps it with
// bench/compose under Harbor's conventions: the task image stays the
// rewritten environment/Dockerfile, every other service runs beside it on
// the run's internal network, and the snapshot verifier joins that network
// as Harbor's test.sh, run inside main, would reach the services.

// harborComposeFile is the only compose file Harbor reads (harbor 0.23.0,
// environments/docker/docker.py _environment_docker_compose_path).
const harborComposeFile = "docker-compose.yaml"

// composeFiles are the names that make a compose file, read or not.
var composeFiles = []string{"docker-compose.yaml", "docker-compose.yml", "compose.yaml", "compose.yml"}

// composePlan is how a task's compose file imports.
type composePlan struct {
	file  string   // task-relative compose file for task.yaml; empty when none is used
	notes []string // IMPORT.md lines
}

// planCompose maps a task's compose file, or says why the task cannot be
// imported. A compose file under another name than docker-compose.yaml is
// not read by Harbor, so it is left unused here too and the task imports as
// a single container, with a note.
func planCompose(t *upstreamTask, workdir string) (*composePlan, string) {
	cp := &composePlan{}
	if !isFile(filepath.Join(t.EnvDir, harborComposeFile)) {
		for _, name := range composeFiles {
			if isFile(filepath.Join(t.EnvDir, name)) {
				cp.notes = append(cp.notes, fmt.Sprintf("`environment/%s` is not read by Harbor (harbor 0.23.0 reads only `environment/%s`), so it is not used here either: the task runs as one container, as it does under Harbor.", name, harborComposeFile))
			}
		}
		return cp, ""
	}
	rel := envDir + "/" + harborComposeFile
	env := &schemas.TaskEnvironment{Dockerfile: envDir + "/Dockerfile", Context: envDir, WorkspacePath: workdir, BuildNetwork: true, User: "root"}
	res, err := compose.Expand(t.Dir, rel, env, compose.Options{Conventions: compose.ConventionsHarbor})
	if err != nil {
		return nil, "the compose file cannot be read: " + err.Error()
	}
	if refused := res.Refused(); len(refused) > 0 {
		var parts []string
		for i, f := range refused {
			if i == 3 {
				parts = append(parts, fmt.Sprintf("and %d more", len(refused)-i))
				break
			}
			parts = append(parts, f.Where+": "+f.Message)
		}
		return nil, "multi-container compose environment the bench cannot run as written: " + strings.Join(parts, "; ")
	}
	cp.file = rel
	var ids []string
	for _, sv := range res.Services {
		ids = append(ids, sv.ID)
	}
	if len(ids) > 0 {
		cp.notes = append(cp.notes, fmt.Sprintf("`%s` is mapped by `bench/compose` (Harbor conventions): the agent's container is the task image, and %s run beside it on the run's internal network; the snapshot verifier joins that network to reach them, as Harbor's test.sh does from inside main.", rel, strings.Join(ids, ", ")))
	} else {
		cp.notes = append(cp.notes, fmt.Sprintf("`%s` defines only Harbor's main service; it is mapped by `bench/compose` (Harbor conventions) for its environment variables.", rel))
	}
	for _, f := range res.Notes() {
		cp.notes = append(cp.notes, fmt.Sprintf("compose `%s`: %s: %s", f.Where, f.Level, f.Message))
	}
	return cp, ""
}
