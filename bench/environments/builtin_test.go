package environments

import "testing"

func TestBuiltin(t *testing.T) {
	envs := Builtin(DockerWorkspaceConfig{})
	if len(envs) != 1 {
		t.Fatalf("got %d environments, want 1", len(envs))
	}
	if envs[0].GetName() != DockerWorkspaceType || envs[0].GetVersion() == "" {
		t.Fatalf("got %s@%s", envs[0].GetName(), envs[0].GetVersion())
	}
}
