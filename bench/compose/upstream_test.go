package compose

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestUpstreamSkillsBench maps SkillsBench's own compose files when a local
// copy is named by BENCH_SKILLSBENCH_DIR (git clone of
// benchflow-ai/skillsbench); it is skipped otherwise. fix-visual-stability
// has a second service (api) and must map; drone-planning-control reads a
// provider key from the host and must be refused.
func TestUpstreamSkillsBench(t *testing.T) {
	root := os.Getenv("BENCH_SKILLSBENCH_DIR")
	if root == "" {
		t.Skip("BENCH_SKILLSBENCH_DIR is not set")
	}
	env := func(dir string) *schemas.TaskEnvironment {
		return &schemas.TaskEnvironment{Dockerfile: "environment/Dockerfile", Context: "environment", WorkspacePath: "/root", User: "root", BuildNetwork: true}
	}
	fvs := filepath.Join(root, "tasks", "fix-visual-stability")
	res, err := Expand(fvs, "environment/docker-compose.yaml", env(fvs), Options{Conventions: ConventionsHarbor})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Refused()) != 0 || len(res.Services) != 1 || res.Services[0].ID != "api" || res.Services[0].Port != 4000 {
		t.Fatalf("fix-visual-stability: services %+v findings %v", res.Services, res.Findings)
	}
	for _, f := range res.Findings {
		t.Logf("fix-visual-stability %s", f)
	}
	dpc := filepath.Join(root, "tasks", "drone-planning-control")
	res, err = Expand(dpc, "environment/docker-compose.yaml", env(dpc), Options{Conventions: ConventionsHarbor})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res.Refused(), LevelRefused, "services.main.environment[0]", "ANTHROPIC_API_KEY is not set") {
		t.Fatalf("drone-planning-control: %v", res.Findings)
	}
}
