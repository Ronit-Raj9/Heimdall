package engine

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The generic misleading notes probe plans on a SWE-bench-shaped task with
// no notes of its own: its built-in fixture is hashed at the agent's notes
// path under the suite workdir, AGENTS.md when the agent names none, and a
// path out of the workspace is refused.
func TestWorkspaceFilesGenericMisleadingNotes(t *testing.T) {
	suite := &schemas.Suite{ID: "hd-swe-verified", Workdir: "/testbed", Environment: "docker_workspace"}
	task := &schemas.Task{ID: "astropy__astropy-12907", Kind: schemas.TaskBugfix, Dir: t.TempDir()}
	probe := suite.ProbeByID(schemas.GenericProbeMisleadingNotes)
	want := utils.SHA256Bytes([]byte(schemas.GenericNotesContent))
	for pf, key := range map[string]string{"AGENTS.md": "/testbed/AGENTS.md", "": "/testbed/AGENTS.md", "docs/NOTES.md": "/testbed/docs/NOTES.md"} {
		got, err := workspaceFiles(suite, task, probe, &schemas.AgentConfig{ID: "a", Notes: schemas.AgentNotes{ProjectFile: pf}})
		if err != nil {
			t.Fatalf("%q: %v", pf, err)
		}
		if len(got) != 1 || got[key] != want {
			t.Fatalf("%q: files %v, want %s at %s", pf, got, want, key)
		}
	}
	if _, err := workspaceFiles(suite, task, probe, &schemas.AgentConfig{ID: "a", Notes: schemas.AgentNotes{ProjectFile: "../grader/verify.sh"}}); err == nil {
		t.Fatal("notes path out of the workspace must be refused")
	}
}
