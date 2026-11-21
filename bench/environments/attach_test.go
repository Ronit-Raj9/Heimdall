package environments

import (
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAttachNeverStartedIsInfraError reproduces audit 2-B-05: a failed
// "docker start -a" on a container that never ran must not be scored with
// the zero exit code of its initial state.
func TestAttachNeverStartedIsInfraError(t *testing.T) {
	docker, _ := fakeDocker(t, "start -a")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
	dir := t.TempDir()
	cases := map[string]string{
		"never started": "0 created 0001-01-01T00:00:00Z",
		"still running": "0 running 2026-01-01T00:00:00Z",
		"garbage":       "0",
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FAKE_AGENT_STATE", state)
			res, err := e.attach(t.Context(), "c1", dir+"/out", dir+"/err", time.Minute)
			if err == nil {
				t.Fatalf("attach accepted state %q: exit=%d", state, res.ExitCode)
			}
		})
	}
	// QC2-09-02: the confirmed non-start is a pre-launch failure the
	// pipeline retries, not a counted infra_error.
	t.Run("never started is not launched", func(t *testing.T) {
		t.Setenv("FAKE_AGENT_STATE", "0 created 0001-01-01T00:00:00Z")
		_, err := e.attach(t.Context(), "c1", dir+"/out", dir+"/err", time.Minute)
		if !errors.Is(err, schemas.ErrNotLaunched) {
			t.Fatalf("err %v, want ErrNotLaunched", err)
		}
		t.Setenv("FAKE_AGENT_STATE", "0 running 2026-01-01T00:00:00Z")
		if _, err := e.attach(t.Context(), "c1", dir+"/out", dir+"/err", time.Minute); errors.Is(err, schemas.ErrNotLaunched) {
			t.Fatalf("a started container is marked not launched: %v", err)
		}
	})
	t.Run("exited nonzero kept", func(t *testing.T) {
		t.Setenv("FAKE_AGENT_STATE", "3 exited 2026-01-01T00:00:00Z")
		res, err := e.attach(t.Context(), "c1", dir+"/out", dir+"/err", time.Minute)
		if err != nil || res.ExitCode != 3 {
			t.Fatalf("got %v, %v", res, err)
		}
	})
}
