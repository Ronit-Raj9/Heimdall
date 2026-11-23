package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

const loopAgentYAML = `id: loop
adapter: command
image: bench/agent-loop:1
build: {dockerfile: agents/images/loop/Dockerfile, tag: "bench/agent-loop:1", base_arg: BASE_IMAGE}
argv: [agent]
env: {}
model: gpt-test
budgets: {wall_s: 900, cpus: 2, mem_gb: 4}
gateway: {provider: openai, max_retries: 0}
`

// The shared worker takes agent.build jobs (N181): it builds the agent's
// container for the named benchmark in process, under the org's policy,
// streams the build to the job log and prints the tag and image id the
// server reads.
func TestSharedRunnerRunsAgentBuildJob(t *testing.T) {
	bin, calls := fakeDockerBin(t, "echo 'Step 1/3 : FROM bench/s-base:1'")
	project, _ := orgAgentProject(t)
	if err := os.WriteFile(filepath.Join(project, "agents", "loop.yaml"), []byte(loopAgentYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: utils.OrgImagePrefix("org_a"),
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}}
	x := &sharedAgentBuildExecutor{docker: utils.Docker{Binary: bin, Policy: pol}, root: project,
		loadSuite: func(_ context.Context, ref string) (*schemas.Suite, error) {
			return &schemas.Suite{ID: ref, BaseImage: schemas.ImageSpec{Tag: "bench/" + ref + "-base:1"}}, nil
		}}
	jx := newSharedJobExecutor(nil, x)
	var lines []string
	log := func(l string) { lines = append(lines, l) }
	job := &schemas.JobLease{JobID: "j1", JobKind: kindAgentBuild, Argv: []string{"agent", "build", "-config", cliConfig, "-suite", "s", "agents/loop.yaml"}}
	run, err := jx.RunJob(context.Background(), job, log)
	if err != nil {
		t.Fatalf("%v (log %q)", err, lines)
	}
	build := ""
	for _, l := range strings.Split(readTestFile(t, calls), "\n") {
		if strings.HasPrefix(l, "build ") {
			t.Fatalf("agent built on the host daemon: %s", l)
		}
		if strings.HasPrefix(l, "buildx build") {
			build = l
		}
	}
	if !strings.HasPrefix(build, "buildx build --builder heimdall-org-builder") || !strings.Contains(build, "-t org-org-a/bench/agent-loop:1-s") {
		t.Fatalf("build %q", build)
	}
	res := agentBuildResult(lines, nil)
	m, _ := res.(map[string]any)
	if m["tag"] != "bench/agent-loop:1-s" || m["image_id"] != "sha256:built" {
		t.Fatalf("result %v from log %q", res, lines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Step 1/3 : FROM bench/s-base:1") {
		t.Fatalf("build output not streamed: %q", lines)
	}
	var out map[string]string
	if run == nil || json.Unmarshal(run.Result, &out) != nil || out["built_on"] != "shared worker" {
		t.Fatalf("run %+v", run)
	}
	// Paths outside agents/ and other kinds are refused.
	for _, argv := range [][]string{
		{"agent", "build", "-suite", "s", "../etc/passwd.yaml"},
		{"agent", "build", "-suite", "s", "policies/x.yaml"},
		{"agent", "check", "agents/loop.yaml"},
	} {
		if _, err := jx.RunJob(context.Background(), &schemas.JobLease{JobKind: kindAgentBuild, Argv: argv}, log); err == nil {
			t.Errorf("argv %v accepted", argv)
		}
	}
	if _, err := jx.RunJob(context.Background(), &schemas.JobLease{JobKind: "scan.mcp"}, log); err == nil {
		t.Error("scan.mcp accepted by the shared worker")
	}
}

// A hosted agent.build reaches the shared worker, which takes only some job
// kinds: the server must not answer "No worker online" when that worker is
// live (N181). Kinds it does not take still have no runner.
func TestSharedRunnerAgentBuildJobKindIsOffered(t *testing.T) {
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := engine.NewCoordinator(&schemas.Registry{Store: st}, engine.RemoteOptions{Token: "worker-secret-0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Register(context.Background(), &schemas.WorkerRegister{WorkerID: "shared-1", Version: engine.BenchVersion, Slots: 1,
		JobKinds: []string{issuebench.WorkJobKind, kindAgentBuild}}); err != nil {
		t.Fatal(err)
	}
	if !jobRunnerOnline(c, kindAgentBuild) {
		t.Fatal("agent.build: the live shared worker is not counted")
	}
	if jobRunnerOnline(c, "scan.mcp") || jobRunnerOnline(nil, kindAgentBuild) {
		t.Fatal("a kind the shared worker does not take counted it")
	}
}
