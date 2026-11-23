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
