package environments

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// inUseDocker wraps fakeDocker: `volume rm` of a volume some earlier
// `create` mounted fails, as the docker daemon refuses an in-use volume.
func inUseDocker(t *testing.T) (utils.Docker, string) {
	t.Helper()
	base, logPath := fakeDocker(t)
	dir := t.TempDir()
	script := `#!/bin/sh
log=` + logPath + `
if [ "$1 $2" = "volume rm" ]; then
  for v in "$@"; do
    case "$v" in -f|volume|rm) continue;; esac
    if [ -f "$log" ] && grep '^create ' "$log" | grep -q -- "-v $v:"; then
      echo "volume is in use" >&2; exit 1
    fi
  done
fi
exec ` + base.Binary + ` "$@"
`
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func teamRC(t *testing.T) *schemas.RunContext {
	return &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_1"},
		Suite:  &schemas.Suite{},
		Task:   &schemas.Task{ID: "t", Environment: &schemas.TaskEnvironment{Env: map[string]string{"TASK_VAR": "from-task"}}},
		Agent:  &schemas.AgentConfig{},
		Handle: &schemas.EnvironmentHandle{Volume: "bench-ws-r_1", AuxVolume: "bench-aux-r_1"},
		Dir:    t.TempDir(),
	}
}

// TestExecTeamSharedFolderBothMembersLaunch is the 2-B-04 regression: two
// members with the same plan folder each get their own folder volume, so the
// second never removes the first member's mounted volume.
func TestExecTeamSharedFolderBothMembersLaunch(t *testing.T) {
	docker, logPath := inUseDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "evalnet"})
	skills := t.TempDir()
	rc := teamRC(t)
	plan := func() *schemas.RunPlan {
		return &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}, Dirs: map[string]string{"/opt/skills": skills}}
	}
	members := []schemas.MemberRun{
		{Member: schemas.TeamMember{ID: "lead"}, Plan: plan()},
		{Member: schemas.TeamMember{ID: "w1"}, Plan: plan()},
	}
	if _, err := e.ExecTeam(t.Context(), rc, members); err != nil {
		t.Fatalf("exec team: %v", err)
	}
	all := calls(t, logPath)
	for _, want := range []struct {
		prefix string
		parts  []string
	}{
		{"create --name bench-r_1-m-lead", []string{"-v bench-dir-r_1-m-lead-0:/opt/skills:ro"}},
		{"create --name bench-r_1-m-w1", []string{"-v bench-dir-r_1-m-w1-0:/opt/skills:ro"}},
		{"volume create", []string{"bench-dir-r_1-m-w1-0"}},
	} {
		if callWith(all, want.prefix, want.parts...) == "" {
			t.Errorf("no call %q with %q in:\n%s", want.prefix, want.parts, strings.Join(all, "\n"))
		}
	}
}

// TestExecTeamMembersGetTaskEnv is the 2-B-10 regression: a member's launch
// carries the compose task environment, as a single agent's does.
func TestExecTeamMembersGetTaskEnv(t *testing.T) {
	docker, _ := fakeDocker(t)
	var seen []string
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "evalnet"})
	rc := teamRC(t)
	members := []schemas.MemberRun{
		{Member: schemas.TeamMember{ID: "lead"}, Plan: &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin"}}},
		{Member: schemas.TeamMember{ID: "w1"}, Plan: &schemas.RunPlan{Image: "agent:1", Argv: []string{"run"}, Env: map[string]string{"PATH": "/usr/bin", "TASK_VAR": "mine"}}},
	}
	envFileHook = func(lines []string) { seen = append(seen, strings.Join(lines, "\n")) }
	t.Cleanup(func() { envFileHook = nil })
	if _, err := e.ExecTeam(t.Context(), rc, members); err != nil {
		t.Fatalf("exec team: %v", err)
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "TASK_VAR=from-task") || !strings.Contains(seen[1], "TASK_VAR=mine") {
		t.Fatalf("member env files: %q", seen)
	}
}

// TestWriteEnvFileSweepsDeadProcessFiles is the 2-B-14 regression: env files
// are staged in a private folder, and a file a dead process left is removed.
func TestWriteEnvFileSweepsDeadProcessFiles(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir, err := envStageDir()
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", st.Mode())
	}
	dead := filepath.Join(dir, "env-999999999-x")
	live := filepath.Join(dir, "env-1-x") // init never dies
	for _, p := range []string{dead, live} {
		if err := os.WriteFile(p, []byte("K=v\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sweepEnvFiles(dir)
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead process env file kept: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("live process env file removed: %v", err)
	}
	f, err := writeEnvFile([]string{"A=b"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f)
	if filepath.Dir(f) != dir {
		t.Fatalf("env file %s outside %s", f, dir)
	}
}

// TestDestroyForgetsCollector is the 2-B-13 regression: Destroy drops both
// collector entries of the run, whether its collector started or not.
func TestDestroyForgetsCollector(t *testing.T) {
	docker, _ := fakeDocker(t)
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker, HelperImage: "helper:1", EvalNetwork: "evalnet"})
	for _, id := range []string{"r_ok", "r_failed"} {
		collectorTried.Store(id, true)
	}
	collectorRuns.Store("r_ok", true)
	for _, id := range []string{"r_ok", "r_failed"} {
		if err := e.Destroy(t.Context(), &schemas.RunContext{Run: &schemas.Run{RunID: id}}); err != nil {
			t.Fatal(err)
		}
		if _, ok := collectorRuns.Load(id); ok {
			t.Errorf("%s: collectorRuns kept", id)
		}
		if _, ok := collectorTried.Load(id); ok {
			t.Errorf("%s: collectorTried kept", id)
		}
	}
}
