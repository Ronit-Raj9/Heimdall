package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A manifest records a per-suite agent's derived tag (bench/agent-c:1-s) as
// its image. The local prewarm still builds it on the suite's base image
// from the agent's Dockerfile instead of skipping it (fix wave 3, H01 note).
func TestPrewarmAgentImagesPerSuiteFromManifestTag(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"C": {Config: schemas.AgentConfig{ID: "c", Image: "bench/agent-c:1-s", Build: &schemas.ImageSpec{Dockerfile: "x/Dockerfile", Tag: "bench/agent-c:1", BaseArg: "BASE"}}},
	}}
	s := &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "bench/s-base:1"}}
	imgs := prewarmAgentImages(m, s, "/root")
	if len(imgs) != 1 || imgs[0].Tag != "bench/agent-c:1-s" || imgs[0].Dockerfile == "" || !imgs[0].PerSuite {
		t.Fatalf("images %+v, want one per-suite build of bench/agent-c:1-s", imgs)
	}
}
