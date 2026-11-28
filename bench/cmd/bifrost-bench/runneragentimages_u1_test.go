package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// The live defect: the readiness plan of a user-made benchmark runs a
// per-suite agent (bare-model) on the benchmark's own base image, which the
// org built as org-org-a/bench/s-base:1. The agent build starts from it,
// copied to the registry under the org's namespace and pinned by digest.
// The scripted control agents build nothing: they run the suite base image
// directly, so they never meet the base image refusal.
func TestEnsureAgentImagesOnOwnSuiteBase(t *testing.T) {
	bin, calls := fakeDockerBin(t, "")
	project, df := orgAgentProject(t)
	prefix := utils.OrgImagePrefix("org_a")
	id := "sha256:" + strings.Repeat("1", 64)
	digest := "sha256:" + strings.Repeat("d", 64)
	var pushed []string
	pol := &utils.DockerPolicy{OrgID: "org_a", Builder: "heimdall-org-builder", TagPrefix: prefix,
		Trusted: []string{t.TempDir()}, Untrusted: []string{project}, Registry: "registry:5000",
		Pinned: func(string) (string, error) { return "", utils.ErrNotPinned },
		Publish: func(context.Context, string) (string, error) {
			t.Fatal("the org's own base went to the platform publisher")
			return "", nil
		},
		OwnImage: func(_ context.Context, ref string) (string, bool, error) {
			return id, ref == prefix+"bench/s-base:1", nil
		},
		PublishOwn: func(_ context.Context, hostRef, pushRef string) (string, error) {
			pushed = append(pushed, hostRef+" "+pushRef)
			return digest, nil
		},
	}
	x := newSharedAgentImages(utils.Docker{Binary: bin, Policy: pol})
	m, s := perSuiteManifest(df)
	m.Arms["ctl"] = schemas.ArmManifest{ConfigHash: "h3", Config: schemas.AgentConfig{ID: "control-noop", Adapter: "scripted"}}
	m.Slots = append(m.Slots, schemas.Slot{Arm: "ctl", Task: "t1"})
	if err := x.ensure(context.Background(), m, s, project); err != nil {
		t.Fatal(err)
	}
	push := prefix + "bench/s-base:" + strings.Repeat("1", 64)
	if len(pushed) != 1 || pushed[0] != prefix+"bench/s-base:1 "+push {
		t.Fatalf("pushed %v", pushed)
	}
	if !strings.Contains(readTestFile(t, calls), "--build-context bench/s-base:1=docker-image://registry:5000/"+push+"@"+digest) {
		t.Fatalf("no own base context: %s", readTestFile(t, calls))
	}
	if n := countCalls(t, calls, "buildx build"); n != 1 {
		t.Fatalf("%d builds, want 1 (scripted controls build nothing)", n)
	}
}
