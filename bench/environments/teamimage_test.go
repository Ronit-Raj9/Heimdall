package environments

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCheckFrozenMemberImage reproduces audit 2-B-09: a team member running
// its own image tag must be refused when the tag names another image than
// the one the manifest froze for that member.
func TestCheckFrozenMemberImage(t *testing.T) {
	docker, _ := fakeDocker(t) // image inspect answers Id sha256:2222
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
	rc := &schemas.RunContext{
		Slot: &schemas.Slot{Arm: "a"},
		Manifest: &schemas.Manifest{ExpID: "e", Arms: map[string]schemas.ArmManifest{
			"a": {Members: map[string]schemas.MemberManifest{"w": {ImageID: "sha256:" + strings.Repeat("9", 64)}}},
		}},
	}
	err := e.checkFrozenMemberImage(t.Context(), rc, "w", "member:1")
	if err == nil || !strings.Contains(err.Error(), "member image member:1") {
		t.Fatalf("drifted member image accepted: %v", err)
	}
	rc.Manifest.Arms["a"].Members["w"] = schemas.MemberManifest{ImageID: "sha256:2222"}
	if err := e.checkFrozenMemberImage(t.Context(), rc, "w", "member:1"); err != nil {
		t.Fatalf("frozen member image refused: %v", err)
	}
	if err := e.checkFrozenMemberImage(t.Context(), rc, "lead", "arm:1"); err != nil {
		t.Fatalf("member without its own config refused: %v", err)
	}
}
