package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// A template agent's plain build (bench/agent-<id>:1, no base_arg) is built
// on the shared worker before its slots run (M1). Two versions that differ
// only in settings share it and build once; a version with its own tag (a
// different build, handlers.versionArmTags) builds its own image.
func TestEnsureAgentImagesBuildsPlainTemplateBuild(t *testing.T) {
	bin, calls := fakeDockerBin(t, "")
	project, _ := orgAgentProject(t)
	df := filepath.Join(project, "agents", "images", "bot", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(df), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(df, []byte("FROM busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: utils.OrgImagePrefix("org_a"),
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	x := newSharedAgentImages(utils.Docker{Binary: bin, Policy: pol})
	arm := func(hash, tag string, wall int) schemas.ArmManifest {
		return schemas.ArmManifest{ConfigHash: hash, Config: schemas.AgentConfig{ID: "bot", Adapter: "command", Image: tag,
			Budgets: schemas.Budgets{WallS: wall}, Build: &schemas.ImageSpec{Dockerfile: "agents/images/bot/Dockerfile", Tag: tag}}}
	}
	s := &schemas.Suite{ID: "hd-swe-verified"}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": arm("h1", "bench/agent-bot:1", 600), "B": arm("h2", "bench/agent-bot:1", 900)},
		Slots: []schemas.Slot{{Arm: "A", Task: "t1"}, {Arm: "B", Task: "t1"}}}
	if err := x.ensure(context.Background(), m, s, project); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(t, calls, "buildx build --builder heimdall-org-builder"); n != 1 {
		t.Fatalf("%d builds, want 1: %s", n, readTestFile(t, calls))
	}
	if !strings.Contains(readTestFile(t, calls), "-t org-org-a/bench/agent-bot:1") {
		t.Fatalf("plain build not tagged under the org namespace: %s", readTestFile(t, calls))
	}
	// The next lease of another settings-only version builds nothing.
	m.Arms["B"] = arm("h3", "bench/agent-bot:1", 1200)
	if err := x.ensure(context.Background(), m, s, project); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(t, calls, "buildx build"); n != 1 {
		t.Fatalf("settings-only version rebuilt: %d builds", n)
	}
	// A version with its own tag builds its own image.
	m.Arms["B"] = arm("h4", "bench/agent-bot:1-v3", 600)
	if err := x.ensure(context.Background(), m, s, project); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(t, calls, "buildx build"); n != 2 || !strings.Contains(readTestFile(t, calls), "bench/agent-bot:1-v3") {
		t.Fatalf("own-tag version: %d builds: %s", n, readTestFile(t, calls))
	}
}
