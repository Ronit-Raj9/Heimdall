package handlers

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildEvaluationCacheAB(t *testing.T) {
	row := func(id string, hit bool, lat string) string {
		h := "false"
		if hit {
			h = "true"
		}
		return `{"id":"` + id + `","kind":"message","provider":"openai","model_requested":"m","model_served":"m","fallback_index":0,"status":"success","latency_ms":` + lat + `,"stream":false,"cache_hit":` + h + `,"timestamp":"2026-11-18T00:00:00Z"}`
	}
	run := func(id, arm string) RunView {
		return RunView{Slot: schemas.Slot{SlotID: "s_" + id, ExpID: "e_c", Arm: arm, Task: "t1", Condition: "clean", Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_c", Attempt: 1}, Sealed: true}
	}
	ok := `{"verified_success":{"state":"observed","v":1,"evidence":"outcome"},"cost_usd":{"state":"observed","v":0.02,"evidence":"gateway"}}`
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_c": {ExpID: "e_c"}}, runs: []RunView{run("r_off", "A"), run("r_on", schemas.EvaluationCacheArm)},
		files: map[string]map[string][]byte{
			"r_off": {FileMetrics: []byte(ok), FileEvidence: []byte(`{"requests":{"complete":true,"requests":[` + row("a", false, "100") + `]}}`)},
			"r_on":  {FileMetrics: []byte(ok), FileEvidence: []byte(`{"requests":{"complete":true,"requests":[` + row("b", true, "10") + `]}}`)},
		}}
	ev := &schemas.Evaluation{ID: "ev_x", Spec: schemas.EvaluationSpec{BifrostCache: true, Arms: []schemas.EvaluationArm{{Label: "A"}}},
		Children: []schemas.EvaluationChild{{Benchmark: "b", ExperimentID: "e_c"}}}
	got := BuildEvaluationCacheAB(LeaderboardSource(idx), ev)
	if !got.Enabled || len(got.Children) != 1 {
		t.Fatalf("got %+v", got)
	}
	rep := got.Children[0].Report
	if rep.Tasks != 1 || rep.On.CacheHits != 1 || rep.Off.CacheHits != 0 || rep.LatencyDeltaMS >= 0 {
		t.Fatalf("report %+v", rep)
	}
	ev.Spec.BifrostCache = false
	if off := BuildEvaluationCacheAB(LeaderboardSource(idx), ev); off.Enabled || len(off.Children) != 0 {
		t.Fatalf("off %+v", off)
	}
}

// D5: in new evaluations the cache-on copy is arm B, found by its label.
func TestBuildEvaluationCacheABArmB(t *testing.T) {
	row := func(id string, hit bool, lat string) string {
		h := "false"
		if hit {
			h = "true"
		}
		return `{"id":"` + id + `","kind":"message","provider":"openai","model_requested":"m","model_served":"m","fallback_index":0,"status":"success","latency_ms":` + lat + `,"stream":false,"cache_hit":` + h + `,"timestamp":"2026-11-18T00:00:00Z"}`
	}
	run := func(id, arm string) RunView {
		return RunView{Slot: schemas.Slot{SlotID: "s_" + id, ExpID: "e_c", Arm: arm, Task: "t1", Condition: "clean", Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_c", Attempt: 1}, Sealed: true}
	}
	ok := `{"verified_success":{"state":"observed","v":1,"evidence":"outcome"},"cost_usd":{"state":"observed","v":0.02,"evidence":"gateway"}}`
	m := &schemas.Manifest{ExpID: "e_c", Arms: map[string]schemas.ArmManifest{"A": {Label: "x on m"}, "B": {Label: "x on m" + schemas.EvaluationCacheSuffix}}}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_c": m}, runs: []RunView{run("r_off", "A"), run("r_on", "B")},
		files: map[string]map[string][]byte{
			"r_off": {FileMetrics: []byte(ok), FileEvidence: []byte(`{"requests":{"complete":true,"requests":[` + row("a", false, "100") + `]}}`)},
			"r_on":  {FileMetrics: []byte(ok), FileEvidence: []byte(`{"requests":{"complete":true,"requests":[` + row("b", true, "10") + `]}}`)},
		}}
	ev := &schemas.Evaluation{ID: "ev_x", Spec: schemas.EvaluationSpec{BifrostCache: true, Arms: []schemas.EvaluationArm{{Label: "A"}}},
		Children: []schemas.EvaluationChild{{Benchmark: "b", ExperimentID: "e_c"}}}
	got := BuildEvaluationCacheAB(LeaderboardSource(idx), ev)
	if len(got.Children) != 1 || got.Children[0].Report.On.CacheHits != 1 || got.Children[0].Report.Off.CacheHits != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestEvaluationBifrostOptionRows(t *testing.T) {
	sum := BifrostUse{GatewayConfigured: true, Reachable: true, LoadedPlugins: []string{"governance", HeimdallPluginName}}
	sum.Features = BifrostFeatures(sum)
	if featureState(sum.Features, "heimdall_plugin") != BifrostFeatureOn {
		t.Fatalf("heimdall plugin not detected: %+v", sum.Features)
	}
	ev := &schemas.Evaluation{ID: "ev_x", Spec: schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{{Label: "A", Model: "openai/gpt-4o"}},
		BifrostCache: true, MCPRoute: schemas.MCPRouteBifrost,
		Failover: &schemas.FailoverOption{Enabled: true, Fallbacks: []schemas.RouteTarget{{Provider: "openai", Model: "gpt-4o-mini"}}}}}
	uses := EvaluationBifrostFeatures(ev, sum)
	for _, id := range []string{"fallbacks", "semantic_cache", "mcp_gateway"} {
		if featureState(uses, id) != BifrostFeatureOn {
			t.Fatalf("%s not on: %+v", id, uses)
		}
	}
}

// The gateway lists the plugin by the name plugins/heimdall registers.
func TestHeimdallPluginNameMatchesPlugin(t *testing.T) {
	src, err := os.ReadFile("../../plugins/heimdall/plugin.go")
	if err != nil {
		t.Skip("plugins/heimdall not in this checkout")
	}
	if !strings.Contains(string(src), `const PluginName = "`+HeimdallPluginName+`"`) {
		t.Fatalf("plugins/heimdall PluginName differs from %q", HeimdallPluginName)
	}
}
