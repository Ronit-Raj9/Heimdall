package sandbox

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSandboxRefuseSnapshotAndTaskImage covers N216 fix 3: a BFCL-shaped
// benchmark (snapshot graded, each task brings its own image) is refused
// with both plain reasons before launch, and a coding-core-shaped one (a
// repository task graded on its diff) is accepted.
func TestSandboxRefuseSnapshotAndTaskImage(t *testing.T) {
	codex := &schemas.AgentConfig{Adapter: "codex"}
	bfcl := environments.DriverSlot{
		Suite: &schemas.Suite{ID: "hd-harbor-bfcl", Verifier: schemas.SuiteVerifier{Mode: schemas.VerifierSnapshot}},
		Task:  &schemas.Task{ID: "t01", Environment: &schemas.TaskEnvironment{Image: "bench/task-bfcl-t01:1"}},
		Agent: codex,
	}
	why := Refuse(bfcl)
	if !slices.Contains(why, RefuseSnapshot) || !slices.Contains(why, RefuseTaskImage) {
		t.Fatalf("BFCL refusals = %q", why)
	}
	plain := PlainRefusals(why)
	for _, want := range []string{
		"it is graded from a copy of the whole machine, which a Modal sandbox cannot make",
		"its tasks bring their own machine image",
	} {
		if !slices.Contains(plain, want) {
			t.Errorf("plain reasons %q miss %q", plain, want)
		}
	}
	core := environments.DriverSlot{Suite: &schemas.Suite{ID: "coding-core"}, Task: &schemas.Task{ID: "t01"}, Agent: codex,
		Probe: &schemas.ProbeSpec{Layer: schemas.LayerGateway}}
	if why := Refuse(core); len(why) != 0 {
		t.Fatalf("coding-core refused: %q", why)
	}
	if got := PlainRefusal("fault layer fixture is not supported in a cloud sandbox (gateway faults are)"); got != "the fixture failure test works on Docker workers only" {
		t.Errorf("fault layer plain = %q", got)
	}
	if got := NeedsRegistry(PlatformModal); got != "Modal needs an image registry it can pull from. Add one on the Modal card." {
		t.Errorf("registry sentence = %q", got)
	}
}
