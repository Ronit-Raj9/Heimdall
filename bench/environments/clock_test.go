package environments

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Z17: the run clock reaches every service; a value the spec sets itself wins.
func TestServiceEnvClock(t *testing.T) {
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r1"}, Suite: &schemas.Suite{}, Task: &schemas.Task{Clock: &schemas.ClockSpec{Start: "2026-03-14T09:00:00Z"}}}
	env := serviceEnv(rc, schemas.ServiceSpec{ID: "a", Env: map[string]string{"FAKETIME": "+0"}})
	if env[schemas.EnvNow] != "2026-03-14T09:00:00Z" || env[schemas.EnvFakeTime] != "+0" {
		t.Fatalf("env %v", env)
	}
	rc.Task.Clock = nil
	if _, ok := serviceEnv(rc, schemas.ServiceSpec{ID: "a"})[schemas.EnvNow]; ok {
		t.Fatal("no clock, no BENCH_NOW")
	}
}
