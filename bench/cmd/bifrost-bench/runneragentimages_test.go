package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDockerBin writes a docker CLI that appends each call's arguments to
// <dir>/calls (one line per call), answers `image inspect` with an image id
// (or "No such image" for a tag listed in <dir>/missing) and runs any
// script given for builds (exit code of the script).
func fakeDockerBin(t *testing.T, buildScript string) (bin, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	bin = filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> " + calls + "\n" +
		"case \"$1 $2\" in\n" +
		"\"image inspect\") for a in \"$@\"; do last=\"$a\"; done\n" +
		"  if [ -f " + filepath.Join(dir, "missing") + " ] && grep -qxF \"$last\" " + filepath.Join(dir, "missing") + "; then echo \"Error response from daemon: No such image: $last\" >&2; exit 1; fi\n" +
		"  echo '{\"Id\":\"sha256:built\",\"Config\":{\"User\":\"\"}}'; exit 0;;\n" +
		"esac\n" + buildScript + "\nexit 0\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, calls
}

// orgAgentProject writes an org-written agent Dockerfile into an org
// project folder and returns the project and the Dockerfile.
func orgAgentProject(t *testing.T) (project, dockerfile string) {
	t.Helper()
	project = t.TempDir()
	dockerfile = filepath.Join(project, "agents", "images", "loop", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(dockerfile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dockerfile, []byte("ARG BASE_IMAGE\nFROM ${BASE_IMAGE}\nRUN true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, dockerfile
}

// The shared worker's planner builds per-task agent images with the org's
// policy docker: an org-written Dockerfile goes to the isolated builder
// under the org's namespace, never `docker build` on the host (SEC-A).
func TestSharedRunnerPlannerBuildsAgentImagesUnderPolicy(t *testing.T) {
	bin, calls := fakeDockerBin(t, "")
	project, df := orgAgentProject(t)
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: utils.OrgImagePrefix("org_a"),
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	docker := utils.Docker{Binary: bin, Policy: pol}
	p := engine.NewPlanner(&schemas.Registry{}, project, nil, sharedPlannerOptions(docker, nil)...)
	b := p.AgentImageBuilder(&schemas.Suite{ID: "s"})
	if b == nil {
		t.Fatal("shared planner has no agent image builder")
	}
	img := engine.AgentImage{Tag: "bench/agent-loop:1-s-t1", Dockerfile: df, Context: filepath.Dir(df), BuildArgs: map[string]string{"BASE_IMAGE": "python:3.12-slim"}}
	if _, err := b.BuildAgentImage(context.Background(), img); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	var build string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "build ") {
			t.Fatalf("org Dockerfile built on the host daemon: %s", line)
		}
		if strings.HasPrefix(line, "buildx build") {
			build = line
		}
	}
	if !strings.HasPrefix(build, "buildx build --builder heimdall-org-builder") || !strings.Contains(build, "-t org-org-a/bench/agent-loop:1-s-t1") {
		t.Fatalf("org build not under the policy: %q (calls %q)", build, raw)
	}
}

// perSuiteManifest is a manifest whose arm A runs a per-suite agent (build
// with base_arg) on suite s, and whose arm B runs a plain image.
func perSuiteManifest(df string) (*schemas.Manifest, *schemas.Suite) {
	build := &schemas.ImageSpec{Dockerfile: df, Tag: "bench/agent-loop:1", BaseArg: "BASE_IMAGE"}
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {ConfigHash: "h1", Config: schemas.AgentConfig{ID: "loop", Image: "bench/agent-loop:1-s", Build: build}},
		"B": {ConfigHash: "h2", Config: schemas.AgentConfig{ID: "plain", Image: "bench/agent-plain:1"}},
	}, Slots: []schemas.Slot{{Arm: "A", Task: "t1"}, {Arm: "B", Task: "t1"}}}
	return m, &schemas.Suite{ID: "s", BaseImage: schemas.ImageSpec{Tag: "bench/s-base:1"}}
}

// countCalls counts the calls whose arguments start with prefix.
func countCalls(t *testing.T, calls, prefix string) int {
	t.Helper()
	raw, _ := os.ReadFile(calls)
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// A per-benchmark agent image is built on the shared worker the first time
// a leased experiment needs it, once per agent and benchmark (N180).
func TestEnsureAgentImagesBuildsPerSuiteOnce(t *testing.T) {
	bin, calls := fakeDockerBin(t, "")
	project, df := orgAgentProject(t)
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: utils.OrgImagePrefix("org_a"),
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	x := newSharedAgentImages(utils.Docker{Binary: bin, Policy: pol})
	m, s := perSuiteManifest(df)
	for i := 0; i < 2; i++ {
		if err := x.ensure(context.Background(), m, s, project); err != nil {
			t.Fatal(err)
		}
	}
	if n := countCalls(t, calls, "buildx build --builder heimdall-org-builder"); n != 1 {
		t.Fatalf("%d builds, want 1", n)
	}
	if !strings.Contains(readTestFile(t, calls), "-t org-org-a/bench/agent-loop:1-s") || !strings.Contains(readTestFile(t, calls), "BASE_IMAGE=bench/s-base:1") {
		t.Fatalf("build not on the suite base under the org namespace: %s", readTestFile(t, calls))
	}
	// An arm whose every task has a per-task image needs no per-suite image.
	m2, s2 := perSuiteManifest(df)
	s2.ID = "s2"
	am := m2.Arms["A"]
	am.TaskImages = map[string]string{"t1": "bench/agent-loop:1-s2-t1"}
	m2.Arms["A"] = am
	if err := x.ensure(context.Background(), m2, s2, project); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(t, calls, "buildx build"); n != 1 {
		t.Fatalf("built a per-suite image no slot runs (%d builds)", n)
	}
}

// An agent made in the web agent builder (its own Dockerfile in the org's
// project, no base_arg) has its image built on the shared worker; before,
// nothing built it and every run failed with "No such image". A built-in
// agent, whose Dockerfile is not in the project, is left alone.
func TestEnsureAgentImagesBuildsOrgOwnImage(t *testing.T) {
	bin, calls := fakeDockerBin(t, "")
	project, df := orgAgentProject(t)
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: utils.OrgImagePrefix("org_a"),
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	x := newSharedAgentImages(utils.Docker{Binary: bin, Policy: pol})
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {ConfigHash: "h1", Config: schemas.AgentConfig{ID: "loop", Image: "bench/agent-loop:1",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/loop/Dockerfile", Tag: "bench/agent-loop:1"}}},
		"B": {ConfigHash: "h2", Config: schemas.AgentConfig{ID: "codex", Image: "bench/agent-codex:1",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"}}},
	}, Slots: []schemas.Slot{{Arm: "A", Task: "t1"}, {Arm: "B", Task: "t1"}}}
	s := &schemas.Suite{ID: "s"}
	for i := 0; i < 2; i++ {
		if err := x.ensure(context.Background(), m, s, project); err != nil {
			t.Fatal(err)
		}
	}
	got := readTestFile(t, calls)
	if n := countCalls(t, calls, "buildx build --builder heimdall-org-builder"); n != 1 {
		t.Fatalf("%d builds, want 1: %s", n, got)
	}
	if !strings.Contains(got, "-t org-org-a/bench/agent-loop:1") || !strings.Contains(got, filepath.Dir(df)) {
		t.Fatalf("org agent image not built from its project Dockerfile under the org namespace: %s", got)
	}
	if strings.Contains(got, "agent-codex") {
		t.Fatalf("built-in agent image built: %s", got)
	}
}

// A failed build refuses the lease with the agent, the benchmark and the
// last build lines, so the slots end with that reason instead of waiting.
func TestEnsureAgentImagesFailureRefusesLeaseWithReason(t *testing.T) {
	bin, _ := fakeDockerBin(t, "case \"$1\" in buildx|build) echo 'Step 3/5 : RUN pip install -r requirements.txt'; echo 'ERROR: No matching distribution found for openai==3.50.0' >&2; exit 1;; esac")
	project, df := orgAgentProject(t)
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "b", TagPrefix: utils.OrgImagePrefix("org_a"), Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	x := newSharedAgentImages(utils.Docker{Binary: bin, Policy: pol})
	m, s := perSuiteManifest(df)
	err := x.ensure(context.Background(), m, s, project)
	if err == nil {
		t.Fatal("a failed build did not refuse the plan")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "prepare the agent container bench/agent-loop:1-s for s: ") || !strings.Contains(msg, "No matching distribution found for openai==3.50.0") {
		t.Fatalf("reason %q", msg)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
