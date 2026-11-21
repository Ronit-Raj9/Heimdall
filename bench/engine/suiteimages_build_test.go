package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestSuiteImageTagsUseTheLaunchTag is QC2-09-04: a locally built task
// (compose) service without an image tag is frozen under the
// content-derived tag its launch starts, the same tag utils.ServiceImage
// gives the environment.
func TestSuiteImageTagsUseTheLaunchTag(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sidecar"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sidecar", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sv := schemas.ServiceSpec{ID: "side", Build: &schemas.ImageSpec{Dockerfile: "sidecar/Dockerfile"}}
	s := &schemas.Suite{ID: "s", Tasks: []*schemas.Task{{ID: "t1", Dir: dir, Services: []schemas.ServiceSpec{sv}}}}
	want, err := utils.ServiceImage(sv, dir, "s", false)
	if err != nil {
		t.Fatal(err)
	}
	got := suiteImageTags(s, nil)[TaskServiceRole("t1", "side")]
	if got == "" || got != want.Tag || !strings.HasPrefix(got, "bench/svc-s-side:") {
		t.Fatalf("frozen tag %q, launch tag %q", got, want.Tag)
	}
}
