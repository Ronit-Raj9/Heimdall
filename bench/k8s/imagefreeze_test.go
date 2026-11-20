package k8s

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

type localDriver = environments.Driver

// tagLocal answers image lookups for one mutable tag on the controller.
type tagLocal struct {
	localDriver
	id      string
	digests []string
}

func (l tagLocal) ImageID(context.Context, string) (string, error) { return l.id, nil }
func (l tagLocal) ImageRepoDigests(context.Context, string) ([]string, error) {
	return l.digests, nil
}

// TestAgentImageFrozenRejectsRetaggedDigest reproduces audit 2-D-01: when
// the tag was rebuilt and pushed after planning, the node runs the new
// registry digest and the tag's current digests list it; that must not pass
// as the frozen image.
func TestAgentImageFrozenRejectsRetaggedDigest(t *testing.T) {
	const frozen = "sha256:old"
	nodeID := "registry.example/agent@sha256:new"
	noDocker := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	moved := &Driver{cfg: Config{Docker: noDocker, Local: tagLocal{id: "sha256:newlocal", digests: []string{"registry.example/agent@sha256:new"}}}}
	if moved.agentImageFrozen(t.Context(), &record{}, nodeID, frozen, "agent:1") {
		t.Fatal("a retagged image was accepted under the frozen manifest")
	}
	same := &Driver{cfg: Config{Docker: noDocker, Local: tagLocal{id: frozen, digests: []string{"registry.example/agent@sha256:new"}}}}
	if !same.agentImageFrozen(t.Context(), &record{}, nodeID, frozen, "agent:1") {
		t.Fatal("the frozen image's own registry digest was refused")
	}
	if !moved.agentImageFrozen(t.Context(), &record{}, frozen, frozen, "agent:1") {
		t.Fatal("the frozen id itself was refused")
	}
}

// TestFrozenEvaluatorImages reproduces audit 2-D-02: the helper, base and
// collector images must be checked against the manifest's frozen ids on
// Kubernetes too, and the collector must be a frozen role even without
// services.
func TestFrozenEvaluatorImages(t *testing.T) {
	noDocker := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	d := &Driver{cfg: Config{Docker: noDocker, HelperImage: "helper:1", EventsCollector: true, CollectorImage: "bench/bifrost-bench:dev",
		Logger: schemas.NoopLogger{}, Local: tagLocal{id: "sha256:rebuilt"}}}
	if roles := d.ImageRoles(); roles["helper"] != "helper:1" || roles[roleCollector] != "bench/bifrost-bench:dev" {
		t.Fatalf("image roles = %v", roles)
	}
	rc := &schemas.RunContext{Suite: &schemas.Suite{BaseImage: schemas.ImageSpec{Tag: "base:1"}},
		Manifest: &schemas.Manifest{ExpID: "e", SuiteImages: map[string]string{"helper": "sha256:frozen"}}}
	if err := d.checkFrozenRoles(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "helper image helper:1") {
		t.Fatalf("rebuilt helper accepted at create: %v", err)
	}
	if err := d.checkNodeRole(t.Context(), rc, &record{}, "helper", "helper:1", ctrPrep, "sha256:rebuilt"); err == nil {
		t.Fatal("rebuilt helper accepted on the node")
	}
	if err := d.checkNodeRole(t.Context(), rc, &record{}, "helper", "helper:1", ctrPrep, "sha256:frozen"); err != nil {
		t.Fatalf("frozen helper refused: %v", err)
	}
	rc.Manifest.SuiteImages = map[string]string{"helper": "sha256:rebuilt"}
	if err := d.checkFrozenRoles(t.Context(), rc); err != nil {
		t.Fatalf("unchanged helper refused: %v", err)
	}
}
