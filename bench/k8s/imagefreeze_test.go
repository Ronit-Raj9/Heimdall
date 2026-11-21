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

// movingTag is a controller whose tag moves right after it was checked: the
// tag still names the frozen id once, then every lookup by the tag sees the
// new image. Lookups by the frozen id see the frozen image.
type movingTag struct {
	localDriver
	frozen string
}

func (l movingTag) ImageID(context.Context, string) (string, error) { return l.frozen, nil }
func (l movingTag) ImageRepoDigests(_ context.Context, ref string) ([]string, error) {
	if ref == l.frozen {
		return []string{"registry.example/agent@sha256:old-repo"}, nil
	}
	return []string{"registry.example/agent@sha256:new"}, nil
}

// TestAgentImageFrozenTagMovesAfterCheck is QC2-10-07: the digest lookup
// uses the checked immutable id, so a tag moved between the identity check
// and the lookup cannot vouch for the new image.
func TestAgentImageFrozenTagMovesAfterCheck(t *testing.T) {
	const frozen = "sha256:old"
	noDocker := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	d := &Driver{cfg: Config{Docker: noDocker, Local: movingTag{frozen: frozen}}}
	if d.agentImageFrozen(t.Context(), &record{}, "registry.example/agent@sha256:new", frozen, "agent:1") {
		t.Fatal("the moved tag's new digest was accepted as the frozen image")
	}
	if !d.agentImageFrozen(t.Context(), &record{}, "registry.example/agent@sha256:old-repo", frozen, "agent:1") {
		t.Fatal("the frozen image's own registry digest was refused")
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
		Manifest: &schemas.Manifest{ExpID: "e", SuiteImages: map[string]string{"helper": "sha256:frozen", roleCollector: "sha256:rebuilt"}}}
	if err := d.checkFrozenRoles(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "helper image helper:1") {
		t.Fatalf("rebuilt helper accepted at create: %v", err)
	}
	if err := d.checkNodeRole(t.Context(), rc, &record{}, "helper", "helper:1", ctrPrep, "sha256:rebuilt"); err == nil {
		t.Fatal("rebuilt helper accepted on the node")
	}
	if err := d.checkNodeRole(t.Context(), rc, &record{}, "helper", "helper:1", ctrPrep, "sha256:frozen"); err != nil {
		t.Fatalf("frozen helper refused: %v", err)
	}
	rc.Manifest.SuiteImages = map[string]string{"helper": "sha256:rebuilt", roleCollector: "sha256:rebuilt"}
	if err := d.checkFrozenRoles(t.Context(), rc); err != nil {
		t.Fatalf("unchanged helper refused: %v", err)
	}
}

// TestFrozenRoleSetDrift is QC2-10-10: a helper or collector configured or
// removed after planning is refused, not skipped because the manifest has
// no id for the current role.
func TestFrozenRoleSetDrift(t *testing.T) {
	noDocker := utils.Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}
	rc := func(images map[string]string) *schemas.RunContext {
		return &schemas.RunContext{Suite: &schemas.Suite{BaseImage: schemas.ImageSpec{Tag: "base:1"}},
			Manifest: &schemas.Manifest{ExpID: "e", SuiteImages: images}}
	}
	drv := func(helper string, collector bool) *Driver {
		return &Driver{cfg: Config{Docker: noDocker, HelperImage: helper, EventsCollector: collector, CollectorImage: "bench/bifrost-bench:dev",
			Logger: schemas.NoopLogger{}, Local: tagLocal{id: "sha256:same"}}}
	}
	for _, tc := range []struct {
		name   string
		d      *Driver
		images map[string]string
		want   string
	}{
		{"helper added", drv("helper:1", false), map[string]string{"base": "sha256:same"}, "was configured"},
		{"helper removed", drv("", false), map[string]string{"base": "sha256:same", "helper": "sha256:same"}, "helper_image was removed"},
		{"collector turned on", drv("", true), map[string]string{"base": "sha256:same"}, "was turned on"},
		{"collector turned off", drv("", false), map[string]string{"base": "sha256:same", roleCollector: "sha256:same"}, "turned off"},
		{"unchanged", drv("helper:1", true), map[string]string{"base": "sha256:same", "helper": "sha256:same", roleCollector: "sha256:same"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.checkFrozenRoles(t.Context(), rc(tc.images))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
