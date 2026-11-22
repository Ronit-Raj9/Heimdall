package main

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPrewarmAgentImages(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Config: schemas.AgentConfig{ID: "a", Image: "bench/agent-a:1", Build: &schemas.ImageSpec{Dockerfile: "agents/images/a/Dockerfile", Tag: "bench/agent-a:1"}}},
		"B": {Config: schemas.AgentConfig{ID: "a2", Image: "bench/agent-a:1", Build: &schemas.ImageSpec{Dockerfile: "agents/images/a/Dockerfile", Tag: "bench/agent-a:1"}}},
		"C": {Config: schemas.AgentConfig{ID: "c", Image: "bench/agent-c:1", Build: &schemas.ImageSpec{Dockerfile: "x/Dockerfile", Tag: "bench/agent-c:1", BaseArg: "BASE"}}},
	}}
	s := &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "bench/s-base:1"}}
	imgs := prewarmAgentImages(m, s, "/root")
	tags := map[string]bool{}
	for _, img := range imgs {
		tags[img.Tag] = true
	}
	// A and B share one image; C is built for the suite on its base image.
	if len(imgs) != 2 || !tags["bench/agent-a:1"] || !tags["bench/agent-c:1-s"] {
		t.Fatalf("images %+v", imgs)
	}
	(&launcher{remoteOnly: true}).Prewarm(m) // a hosted launcher runs no slot here: nothing to do, no panic
	(*launcher)(nil).Prewarm(m)
}
