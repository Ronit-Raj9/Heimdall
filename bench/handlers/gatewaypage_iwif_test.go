package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The Gateway page (ui/app/gateway, ui/components/gateway) was built against
// the types of ui/lib/types/gateway.ts with mocked answers. These tests read
// that file and check each real handler answer against it: every field the
// type requires is in the answer, and the answer has no field the type does
// not name (the top-level "mode" that lib.SendJSON adds is allowed).

// gwPageTypes parses the interfaces of gateway.ts: name -> field -> optional.
func gwPageTypes(t *testing.T) map[string]map[string]bool {
	t.Helper()
	data, err := os.ReadFile("../ui/lib/types/gateway.ts")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]bool{}
	field := regexp.MustCompile(`^\s*([a-z_]+)(\?)?\s*:`)
	for _, m := range regexp.MustCompile(`(?s)export interface (\w+)\s*\{(.*?)\n?\}`).FindAllStringSubmatch(string(data), -1) {
		fields := map[string]bool{}
		for _, part := range strings.FieldsFunc(m[2], func(r rune) bool { return r == ';' || r == '\n' }) {
			if f := field.FindStringSubmatch(part); f != nil {
				fields[f[1]] = f[2] == "?"
			}
		}
		out[m[1]] = fields
	}
	if len(out["GwEffect"]) < 10 || len(out["GwCardLine"]) < 8 {
		t.Fatalf("gateway.ts was not parsed: %v", out)
	}
	return out
}

// gwShape checks obj against the named type's fields; top allows "mode".
func gwShape(t *testing.T, types map[string]map[string]bool, name string, obj any, top bool) map[string]any {
	t.Helper()
	m, ok := obj.(map[string]any)
	if !ok {
		t.Fatalf("%s: answer is not an object: %#v", name, obj)
	}
	return gwShapeFields(t, name, types[name], m, top)
}

func gwShapeFields(t *testing.T, name string, fields map[string]bool, m map[string]any, top bool) map[string]any {
	t.Helper()
	if fields == nil {
		t.Fatalf("no type %s", name)
	}
	for k := range m {
		if _, ok := fields[k]; !ok && !(top && k == "mode") {
			t.Errorf("%s: the server sends %q, which the page type does not name", name, k)
		}
	}
	for f, optional := range fields {
		if _, ok := m[f]; !ok && !optional {
			t.Errorf("%s: the page needs %q, which the server does not send", name, f)
		}
	}
	return m
}

func gwDecode(t *testing.T, st int, out string) map[string]any {
	t.Helper()
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return m
}

// gwJSONTags is the JSON field names of a Go request type.
func gwJSONTags(v any) []string {
	var out []string
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		if tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]; tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func gwKeys(fields map[string]bool) []string {
	var out []string
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func init() {
	low, high := -0.1, 0.2
	solve, cost, lat := 0.5, 0.02, 120.0
	gwfeature.Register(gwfeature.Feature{ID: "iwif_test_shape", Name: "Shape", Compute: func(ctx context.Context, in gwfeature.Input) gwfeature.Effect {
		return gwfeature.Effect{State: gwfeature.StateMeasured, Headline: "measured", Result: "saved", Variant: "B", Tasks: 6,
			SolveDelta: &gwfeature.Interval{Point: 0.05, Low: &low, High: &high}, CostDelta: &gwfeature.Interval{Point: -0.01}, LatencyDelta: &gwfeature.Interval{Point: 3},
			Arms:    []gwfeature.ArmResult{{Arm: "A", Label: "today", Runs: 6, Tasks: 6, SolveRate: &solve, CostPerRun: &cost, LatencyMS: &lat}},
			Details: json.RawMessage(`{"points":[]}`), Setting: &gwfeature.BifrostSetting{Kind: "virtual_key", Title: "Key", Where: "Virtual keys", Body: "{}", Note: "n"},
			Source: gwfeature.Source{Gateway: "v2.5.1", Text: "comparison e_shape"}}
	}})
}

func TestGatewayPageShapeSourceRoutes(t *testing.T) {
	types := gwPageTypes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`"v2.5.1"`)) }))
	defer srv.Close()
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil)
	// The page's save body (gatewayApi.ts setGatewaySource) is what the server reads.
	for _, call := range [][3]string{
		{"GET", "/gateway/source", ""},
		{"POST", "/gateway/source", `{"gateway_url":"` + srv.URL + `","credential_secret":""}`},
		{"GET", "/gateway/source", ""},
		{"POST", "/gateway/source/remove", ""},
	} {
		st, out := secretsCall(h, call[0], APIPrefix+call[1], call[2])
		m := gwShape(t, types, "GwSourceInfo", gwDecode(t, st, out), false)
		if mode := m["mode"]; mode != "none" && mode != "saved" && mode != "run_gateway" {
			t.Fatalf("%s %s: mode %v is not a source mode", call[0], call[1], mode)
		}
	}
}

func TestGatewayPageShapeReportCard(t *testing.T) {
	types := gwPageTypes(t)
	srv, _ := fakeBifrost(t, map[string]string{
		"/api/plugins/loaded": `{"plugins":["semantic_cache"]}`,
		"/api/version":        `"v2.5.1"`,
	})
	root := t.TempDir()
	if err := gwfeature.Save(root, gwfeature.Effect{Feature: schemas.GatewayFeatureSemanticCache, ExpID: "e1", State: gwfeature.StateMeasured, Result: "saved", Headline: "h",
		ComputedAt: time.Now(), Source: gwfeature.Source{Kind: "experiment", ExpID: "e1", Runs: 2, Text: "comparison e1"}}); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root}
	cfg.Gateway.URL = srv.URL
	h := gatewayTestsRouter(OrgEnv{Cfg: cfg, Mode: testMode}, nil)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/report-card", "")
	card := gwShape(t, types, "GwReportCard", gwDecode(t, st, out), true)
	gwShape(t, types, "GwGatewayInfo", card["gateway"], false)
	gwShape(t, types, "GwSourceInfo", card["source"], false)
	lines, _ := card["lines"].([]any)
	if len(lines) < 8 {
		t.Fatalf("lines %v", card["lines"])
	}
	withSource := 0
	for _, l := range lines {
		line := gwShape(t, types, "GwCardLine", l, false)
		if s, ok := line["source"]; ok {
			gwShape(t, types, "GwSource", s, false)
			withSource++
		}
	}
	if withSource == 0 {
		t.Fatal("no line carried the saved effect's source")
	}
	// Plugins is a list even when the gateway is down.
	down := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil)
	st, out = secretsCall(down, "GET", APIPrefix+"/gateway/report-card", "")
	if g := gwShape(t, types, "GwGatewayInfo", gwDecode(t, st, out)["gateway"], false); g["plugins"] == nil {
		t.Fatalf("plugins is null: %s", out)
	}
}

func TestGatewayPageShapeFeatureTests(t *testing.T) {
	types := gwPageTypes(t)
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/features", "")
	list, _ := gwDecode(t, st, out)["features"].([]any)
	if len(list) == 0 {
		t.Fatalf("features %s", out)
	}
	for _, f := range list {
		gwShape(t, types, "GwFeature", f, false)
	}
	// The draft body: every GwDraftRequest field is a field the server reads (it refuses unknown fields).
	if got, want := gwJSONTags(gwfeature.DraftRequest{}), gwKeys(types["GwDraftRequest"]); !reflect.DeepEqual(got, want) {
		t.Fatalf("draft request fields: server %v, page %v", got, want)
	}
	body := `{"feature":"w4p1_test_draft","suite":"swe-lite@1.0.0","base":"agents/codex.yaml","model":"m","provider":"p","tasks":["t1"],"trials":2,"budget_usd":5,"options":{"k":"v"}}`
	st, out = secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", body)
	ans := gwShape(t, types, "GwDraftResponse", gwDecode(t, st, out), true)
	if exp, _ := ans["experiment"].(map[string]any); exp == nil || exp["gateway_test"] == nil {
		t.Fatalf("draft experiment has no gateway_test: %s", out)
	}
}

// gwShapeSource is one feature test comparison e_shape on the shape feature.
func gwShapeSource() gwTestSource {
	m := &schemas.Manifest{ExpID: "e_shape", Arms: map[string]schemas.ArmManifest{"A": {Label: "today"}, "B": {Label: "changed"}}}
	m.Experiment.GatewayTest = &schemas.GatewayTest{Feature: "iwif_test_shape", Control: "A", Variants: []string{"B"}}
	return gwTestSource{manifests: map[string]*schemas.Manifest{"e_shape": m, "e_plain": {ExpID: "e_plain"}}, runs: map[string][]leaderboard.Run{
		"e_shape": {{Slot: schemas.Slot{SlotID: "s1", Task: "t1", Arm: "A", Condition: schemas.ConditionClean, Trial: 1}, Run: schemas.Run{RunID: "r1", Attempt: 1}, Sealed: true}},
	}}
}

func checkEffectShape(t *testing.T, types map[string]map[string]bool, st int, out string) map[string]any {
	t.Helper()
	e := gwShape(t, types, "GwEffect", gwDecode(t, st, out), true)
	if _, ok := e["arms"].([]any); !ok {
		t.Fatalf("arms is not a list: %s", out)
	}
	for _, a := range e["arms"].([]any) {
		gwShape(t, types, "GwArmResult", a, false)
	}
	for _, k := range []string{"solve_delta", "cost_delta", "latency_delta"} {
		if v, ok := e[k]; ok {
			gwShape(t, types, "GwInterval", v, false)
		}
	}
	if v, ok := e["setting"]; ok {
		gwShape(t, types, "GwSetting", v, false)
	}
	src := gwShape(t, types, "GwSource", e["source"], false)
	if src["text"] == "" {
		t.Fatalf("the effect does not say where its numbers come from: %s", out)
	}
	return e
}

func TestGatewayPageShapeEffectAndSetting(t *testing.T) {
	types := gwPageTypes(t)
	src := gwShapeSource()
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}
	h := gatewayTestsRouter(env, src)
	st, out := secretsCall(h, "GET", APIPrefix+"/experiments/e_shape/gateway-effect", "")
	checkEffectShape(t, types, st, out)

	r := router.New()
	sh := newBifrostSettingHandler(env)
	sh.source = func() leaderboard.Source { return src }
	sh.routes(r)
	for _, id := range []string{"e_shape", "e_plain"} {
		st, out = secretsCall(r.Handler, "GET", APIPrefix+"/experiments/"+id+"/bifrost-setting", "")
		ans := gwShape(t, types, "GwBifrostSettingAnswer", gwDecode(t, st, out), true)
		if ans["setting"] != nil {
			gwShape(t, types, "GwSetting", ans["setting"], false)
		} else if ans["reason"] == "" {
			t.Fatalf("%s: no setting and no reason: %s", id, out)
		}
	}
}

func TestGatewayPageShapeOverhead(t *testing.T) {
	types := gwPageTypes(t)
	src := gwShapeSource()
	r := router.New()
	h := newGatewayOverheadHandler(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode})
	h.source = func() leaderboard.Source { return src }
	h.routes(r)
	st, out := secretsCall(r.Handler, "GET", APIPrefix+"/experiments/e_plain/gateway-overhead", "")
	e := checkEffectShape(t, types, st, out)
	if e["state"] != gwfeature.StateNotEnoughData {
		t.Fatalf("no runs: state %v", e["state"])
	}
}

func TestGatewayPageShapeRegisteredAgents(t *testing.T) {
	types := gwPageTypes(t)
	h := gatewayAgentsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, fakeAgentsGet(testRegs, nil), false)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/agents", "")
	list, _ := gwDecode(t, st, out)["agents"].([]any)
	if len(list) != len(testRegs) {
		t.Fatalf("agents %s", out)
	}
	for _, a := range list {
		gwShape(t, types, "GwRegisteredAgent", a, false)
	}
	// The import body and answer are inline types of gatewayApi.ts importGatewayAgent:
	// body { model, provider, id? } (the name is the path), answer { agent_id, yaml }.
	if got := gwJSONTags(gatewayAgentImportRequest{}); !reflect.DeepEqual(got, []string{"id", "model", "provider"}) {
		t.Fatalf("import body fields %v", got)
	}
	st, out = secretsCall(h, "POST", APIPrefix+"/gateway/agents/Support%20Bot/import", `{"model":"gpt-5","provider":"openai"}`)
	gwShapeFields(t, "import answer", map[string]bool{"agent_id": false, "yaml": false}, gwDecode(t, st, out), true)
}

func TestGatewayPageShapePromptAndSkillLists(t *testing.T) {
	types := gwPageTypes(t)
	r, _ := gatewayReposTestRouter(t, fakeBifrostRepos(map[string]string{
		"/api/prompt-repo/prompts":             `{"prompts": [{"id": "p1", "name": "support"}]}`,
		"/api/prompt-repo/prompts/p1/versions": `{"versions": [{"version_number": 3, "commit_message": "first", "created_at": "2026-11-01T00:00:00Z", "model": "gpt-5", "messages": []}]}`,
		"/api/skills":                          `{"skills": [{"id": "s1", "name": "review", "latest_version": "4"}]}`,
		"/api/skills/s1/versions":              `{"versions": [{"version": "3", "skill_md_body": "abcd", "created_at": "2026-11-02T00:00:00Z", "files": []}]}`,
	}))
	for _, c := range []struct{ path, key, typ string }{
		{"/gateway/prompts", "prompts", "GwPrompt"},
		{"/gateway/prompts/p1/versions", "versions", "GwPromptVersion"},
		{"/gateway/skills", "skills", "GwSkill"},
		{"/gateway/skills/s1/versions", "versions", "GwSkillVersion"},
	} {
		st, out := gatewayReposCall(r, "GET", APIPrefix+c.path, "")
		list, _ := gwDecode(t, st, out)[c.key].([]any)
		if len(list) != 1 {
			t.Fatalf("%s: %s", c.path, out)
		}
		gwShape(t, types, c.typ, list[0], false)
	}
}
