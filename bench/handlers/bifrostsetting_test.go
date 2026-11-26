package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// settingSource is a gwTestSource with a newest decision per experiment.
type settingSource struct {
	gwTestSource
	decisions map[string]schemas.Decision
}

func (s settingSource) LatestDecision(id string) (schemas.Decision, bool) {
	d, ok := s.decisions[id]
	return d, ok
}

func init() {
	gwfeature.Register(gwfeature.Feature{ID: "w4p5_test_setting", Name: "Test setting", Compute: func(ctx context.Context, in gwfeature.Input) gwfeature.Effect {
		return gwfeature.Effect{State: gwfeature.StateMeasured, Headline: "measured", Setting: &gwfeature.BifrostSetting{Kind: "virtual_key", Title: "Key", Where: "Virtual keys", Body: "{}"}}
	}})
}

func settingArm(model, provider string, retries int) schemas.ArmManifest {
	a := schemas.ArmManifest{Label: model}
	a.Config.ID, a.Config.Model = "agent", model
	a.Config.Gateway.Provider, a.Config.Gateway.MaxRetries = provider, retries
	a.Config.Gateway.Fallbacks = []string{"openai/gpt-4o"}
	return a
}

func settingRouter(t *testing.T, decision schemas.Decision, arms map[string]schemas.ArmManifest, test *schemas.GatewayTest) fasthttp.RequestHandler {
	t.Helper()
	m := &schemas.Manifest{ExpID: "e_ab12", Arms: arms}
	m.Experiment.GatewayTest = test
	src := settingSource{gwTestSource: gwTestSource{manifests: map[string]*schemas.Manifest{"e_ab12": m}, runs: map[string][]leaderboard.Run{}}, decisions: map[string]schemas.Decision{}}
	if test != nil {
		src.runs["e_ab12"] = []leaderboard.Run{{Slot: schemas.Slot{SlotID: "s1", Task: "t1", Arm: "A", Condition: "clean"}, Run: schemas.Run{RunID: "r1"}, Sealed: true}}
	}
	if decision != "" {
		src.decisions["e_ab12"] = decision
	}
	r := router.New()
	h := newBifrostSettingHandler(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode})
	h.source = func() leaderboard.Source { return src }
	h.routes(r)
	return r.Handler
}

type settingAnswer struct {
	Setting *gwfeature.BifrostSetting `json:"setting"`
	Reason  string                    `json:"reason"`
}

func getSetting(t *testing.T, h fasthttp.RequestHandler) settingAnswer {
	t.Helper()
	st, out := secretsCall(h, "GET", APIPrefix+"/experiments/e_ab12/bifrost-setting", "")
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	if !strings.Contains(out, `"setting":`) {
		t.Fatalf("answer has no setting key: %s", out)
	}
	var a settingAnswer
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestSettingForModelOnlyImproved(t *testing.T) {
	arms := map[string]schemas.ArmManifest{"A": settingArm("claude-sonnet-4-5", "anthropic", 0), "B": settingArm("gpt-4o-mini", "openai", 0)}
	a := getSetting(t, settingRouter(t, schemas.DecisionImproved, arms, nil))
	if a.Setting == nil || a.Setting.Kind != "routing_rule" || a.Reason != "" {
		t.Fatalf("answer = %+v", a)
	}
	var rule gwfeature.RoutingRule
	if err := json.Unmarshal([]byte(a.Setting.Body), &rule); err != nil {
		t.Fatal(err)
	}
	if rule.Name != "heimdall-e_ab12" || rule.Enabled || rule.CelExpression != `model == "claude-sonnet-4-5"` || rule.Scope != "global" || rule.Priority != 10 ||
		len(rule.Targets) != 1 || rule.Targets[0].Provider != "openai" || rule.Targets[0].Model != "gpt-4o-mini" || rule.Targets[0].Weight != 1 ||
		len(rule.Fallbacks) != 1 || rule.Fallbacks[0] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("rule = %+v", rule)
	}
	if a.Setting.Note != "The rule is created switched off. Requests for claude-sonnet-4-5 go to gpt-4o-mini, and back to claude-sonnet-4-5 if gpt-4o-mini fails." {
		t.Fatalf("note = %q", a.Setting.Note)
	}
	a = getSetting(t, settingRouter(t, schemas.DecisionRegressed, arms, nil))
	if a.Setting == nil || !strings.Contains(a.Setting.Body, `"model == \"gpt-4o-mini\""`) {
		t.Fatalf("regressed: A wins: %+v", a)
	}
}

func TestSettingNullWhenInconclusive(t *testing.T) {
	arms := map[string]schemas.ArmManifest{"A": settingArm("m1", "anthropic", 0), "B": settingArm("m2", "openai", 0)}
	a := getSetting(t, settingRouter(t, schemas.DecisionInconclusive, arms, nil))
	if a.Setting != nil || a.Reason != "The comparison could not tell the two setups apart, so there is no setting to change." {
		t.Fatalf("answer = %+v", a)
	}
	for _, d := range []schemas.Decision{schemas.DecisionInvalid, ""} {
		a = getSetting(t, settingRouter(t, d, arms, nil))
		if a.Setting != nil || a.Reason != "The comparison has no valid result yet." {
			t.Fatalf("%q: answer = %+v", d, a)
		}
	}
}

func TestSettingNullWhenArmsDifferInMore(t *testing.T) {
	arms := map[string]schemas.ArmManifest{"A": settingArm("m1", "anthropic", 0), "B": settingArm("m2", "openai", 2)}
	a := getSetting(t, settingRouter(t, schemas.DecisionImproved, arms, nil))
	if a.Setting != nil || a.Reason != "The setups differ in more than the model, so no single Bifrost setting gives this result." {
		t.Fatalf("answer = %+v", a)
	}
}

func TestSettingFromFeatureEffect(t *testing.T) {
	arms := map[string]schemas.ArmManifest{"A": settingArm("m1", "anthropic", 0), "B": settingArm("m1", "anthropic", 0)}
	a := getSetting(t, settingRouter(t, schemas.DecisionInconclusive, arms, &schemas.GatewayTest{Feature: "w4p5_test_setting", Control: "A", Variants: []string{"B"}}))
	if a.Setting == nil || a.Setting.Kind != "virtual_key" || a.Setting.Title != "Key" {
		t.Fatalf("answer = %+v", a)
	}
	h := settingRouter(t, "", arms, nil)
	if st, out := secretsCall(h, "GET", APIPrefix+"/experiments/e_none/bifrost-setting", ""); st != 404 {
		t.Fatalf("unknown comparison: %d %s", st, out)
	}
}
