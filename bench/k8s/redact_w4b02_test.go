package k8s

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// F5.2: the agent's output is cleaned of every runtime credential, not only
// the run key: an org secret value printed by the agent never reaches the
// run folder.
func TestAttachRedactsEveryRunSecret(t *testing.T) {
	f := newFakeAPI(t)
	f.key = "org-secret-value-7f3a"
	d := testDriver(t, f)
	rc := testRun(t)
	rc.Secrets = map[string]string{"API_TOKEN": "org-secret-value-7f3a"}
	rc.Key.Members = []schemas.MemberKey{{ID: "m1", Value: "sk-bf-member-9c2d"}}
	ctx := t.Context()
	h, err := d.Create(ctx, rc)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rc.Handle = h
	plan := &schemas.RunPlan{Image: "bench/coding-core-base:1", Argv: []string{"env"}, WallS: 60}
	res, err := d.Exec(ctx, rc, plan)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	out, _ := os.ReadFile(res.StdoutPath)
	if strings.Contains(string(out), "org-secret-value-7f3a") || !strings.Contains(string(out), "[redacted]") {
		t.Fatalf("stdout kept the org secret: %q", out)
	}
}
