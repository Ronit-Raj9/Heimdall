package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAgentLiveVersionRule: the live version is the last released version;
// before any release, the newest version holding the saved config. GET
// .../versions reports the same.
func TestAgentLiveVersionRule(t *testing.T) {
	r, _, st := versionsRouter(t, nil, "")
	if code, body := abCall(r, "POST", APIPrefix+"/agents/from-template", AgentFromTemplateRequest{Template: "codex", ID: "live-agent"}); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	cfg, _, err := st.Read(context.Background(), projectfs.AreaAgents, "live-agent.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pr := strings.TrimRight(string(cfg), "\n") + "\nagent_version: 9.9.9-live\n"
	if code, body := abCall(r, "POST", APIPrefix+"/agents/live-agent/versions", AgentVersionCreate{YAML: pr}); code != 201 {
		t.Fatalf("register: %d %s", code, body)
	}
	ctx := context.Background()
	rl := &fakeReleases{}
	if got := agentLiveVersion(ctx, rl, st, "", "live-agent"); got.Version != 1 || got.Source != LiveFromSavedConfig {
		t.Fatalf("before a release: %+v, want v1 from the saved config", got)
	}
	rl.recs = append(rl.recs, schemas.ReleaseRecord{ReleaseID: "rel_1", AgentID: "live-agent", Version: 2})
	if got := agentLiveVersion(ctx, rl, st, "", "live-agent"); got.Version != 2 || got.Source != LiveFromRelease || got.ReleaseID != "rel_1" {
		t.Fatalf("after releasing v2: %+v", got)
	}
	if got := agentLiveVersion(ctx, nil, nil, "", "live-agent"); got.Version != 0 {
		t.Fatalf("no services: %+v", got)
	}
	if vs := versionsOf(t, r, "live-agent"); vs.Live != 1 || vs.LiveSource != LiveFromSavedConfig {
		t.Fatalf("versions list without a release log: %+v", vs)
	}
}
