package handlers

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

const hw10AgentDoc = `id: bot
adapter: scripted
image: bench/coding-core-base:1
scripted:
  kind: exploit
model: claude-sonnet-4-5
budgets:
  wall_s: 900
  cpus: 1
  mem_gb: 1
gateway:
  provider: anthropic
`

// N291: version:v1 (and 1, V1) finds the runs of the agent's saved version
// 1, matched by the agent hash as the agent's Versions tab counts them; the
// runs carry no agent_version of their own.
func TestSearchVersionMatchesSavedVersion(t *testing.T) {
	root := t.TempDir()
	if _, _, err := agentversions.Snapshot(root, "bot", []byte(hw10AgentDoc), "ana", "first"); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	var cfg schemas.AgentConfig
	if err := suite.DecodeYAML([]byte(hw10AgentDoc), "agent", suite.SchemaAgent, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg.Model = "gpt-5.4" // an evaluation sets the model per arm; the version ignores it
	ix := &fakeIndex{
		manifests: map[string]*schemas.Manifest{"e1": {ExpID: "e1", SuiteID: "support", Arms: map[string]schemas.ArmManifest{"A": {Config: cfg}}}},
		runs: []RunView{{Slot: schemas.Slot{ExpID: "e1", Task: "t1", Arm: "A"}, Sealed: true, SealHash: "h1",
			Run: schemas.Run{RunID: "r1", Outcome: schemas.OutcomeFail, Agent: schemas.RunAgentInfo{ConfigID: "bot"},
				Timing: schemas.RunTiming{Started: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}}}},
		files: map[string]map[string][]byte{"r1": {}},
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: ix}}
	for _, q := range []string{"q=version:v1", "q=version:1", "q=version:V1", "version=v1", "version=1"} {
		code, res := searchServe(t, env, APIPrefix+"/search?"+q)
		if code != fasthttp.StatusOK || res.Total != 1 || res.Hits[0].Version != "v1" {
			t.Errorf("%s: %d %+v, want the one run of v1", q, code, res)
		}
	}
	if _, res := searchServe(t, env, APIPrefix+"/search?q=version:v2"); res.Total != 0 {
		t.Errorf("version:v2 matched %+v", res)
	}
}
