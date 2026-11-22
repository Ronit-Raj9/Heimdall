package failover

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

func opt(models ...string) Option {
	o := Option{Enabled: true}
	for _, m := range models {
		o.Fallbacks = append(o.Fallbacks, schemas.RouteTarget{Model: m})
	}
	return o
}

func TestPlanFaultProxy(t *testing.T) {
	arms, err := Plan("openai", "gpt-4o", opt("gpt-4o-mini"), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(arms) != 3 || arms[0].Name != ArmClean || arms[1].Name != ArmFailover || arms[2].Name != ArmNoFailover {
		t.Fatalf("arms %+v", arms)
	}
	if arms[0].Fault != nil || arms[0].Policy != nil {
		t.Fatal("clean arm must have no fault and no policy")
	}
	f := arms[1]
	if f.Via != ViaFaultProxy || f.Fault == nil || f.Fault.Model != "gpt-4o" || f.Fault.Status != 503 {
		t.Fatalf("failover fault %+v via %s", f.Fault, f.Via)
	}
	if err := faultproxy.ValidatePlan(*f.Fault); err != nil {
		t.Fatalf("fault proxy refuses plan: %v", err)
	}
	if f.Policy == nil || f.Policy.Strategy != schemas.RoutingFallbackChain || len(f.Policy.Fallbacks) != 1 ||
		f.Policy.Fallbacks[0].Provider != "openai" || f.Policy.Targets[0].Model != "gpt-4o" {
		t.Fatalf("policy %+v", f.Policy)
	}
	if _, ok := f.Headers[HeaderFaultPlan]; ok {
		t.Fatal("fault proxy arm must not send a plugin plan")
	}
	if arms[2].Policy != nil || arms[2].Fault == nil {
		t.Fatal("no-failover arm needs the outage and no fallbacks")
	}
	// The outage must not hit the fallback model.
	if faultproxy.SameModel("gpt-4o-mini", f.Fault.Model) {
		t.Fatal("outage would fail the fallback too")
	}
}

func TestPlanPlugin(t *testing.T) {
	arms, err := Plan("anthropic", "claude-sonnet-4", Option{Enabled: true, OutageStatus: 500,
		Fallbacks: []schemas.RouteTarget{{Provider: "anthropic", Model: "claude-haiku-4"}}}, true, "fp_1")
	if err != nil {
		t.Fatal(err)
	}
	a := arms[1]
	if a.Via != ViaPlugin || a.Headers[HeaderFaultPlanID] != "fp_1" || a.Headers[HeaderArm] != ArmFailover {
		t.Fatalf("headers %+v", a.Headers)
	}
	var p pluginPlan
	if err := json.Unmarshal([]byte(a.Headers[HeaderFaultPlan]), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != 500 || p.Model != "claude-sonnet-4" || p.FromCall != 1 {
		t.Fatalf("plugin plan %+v", p)
	}
	if arms[0].Headers[HeaderFaultPlan] != "" {
		t.Fatal("clean arm sent a plan")
	}
}

func TestValidateRefuses(t *testing.T) {
	cases := map[string]Option{
		"at least one fallback": {Enabled: true},
		"another provider":      {Enabled: true, Fallbacks: []schemas.RouteTarget{{Provider: "anthropic", Model: "claude"}}},
		"repeats the primary":   opt("openai/gpt-4o"),
		"no model":              opt(""),
		"HTTP error status":     {Enabled: true, OutageStatus: 200, Fallbacks: []schemas.RouteTarget{{Model: "m"}}},
	}
	for want, o := range cases {
		err := Validate(o, "openai", "gpt-4o")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
	if _, err := Plan("openai", "gpt-4o", Option{}, false, ""); err == nil {
		t.Error("plan with option off should fail")
	}
}

func usd(v float64) *float64 { return &v }

func row(status string, fb int, lat, cost float64) schemas.GatewayRequest {
	return schemas.GatewayRequest{Kind: "message", Status: status, FallbackIndex: fb, LatencyMS: lat, BifrostUSD: usd(cost)}
}

func TestReportWorked(t *testing.T) {
	r := BuildReport(
		ArmResult{TaskSuccess: true, Requests: []schemas.GatewayRequest{row("success", 0, 100, 0.01), row("success", 0, 100, 0.01)}},
		ArmResult{TaskSuccess: true, Requests: []schemas.GatewayRequest{row("success", 1, 250, 0.004), row("success", 1, 150, 0.004), {Kind: "models"}}},
		ArmResult{TaskSuccess: false, Requests: []schemas.GatewayRequest{row("error", 0, 20, 0)}},
	)
	if !r.FailoverWorked || !r.SuccessWith || r.SuccessWithout {
		t.Fatalf("report %+v", r)
	}
	if r.ExtraLatencyMS != 100 {
		t.Fatalf("extra latency %v", r.ExtraLatencyMS)
	}
	if r.CostChangeUSD == nil || *r.CostChangeUSD > -0.0119 || *r.CostChangeUSD < -0.0121 {
		t.Fatalf("cost change %v", r.CostChangeUSD)
	}
	if !strings.Contains(r.Summary, "did not without fallbacks") {
		t.Fatal(r.Summary)
	}
}

func TestReportFailed(t *testing.T) {
	r := BuildReport(
		ArmResult{TaskSuccess: true, Requests: []schemas.GatewayRequest{row("success", 0, 100, 0.01)}},
		ArmResult{Requests: []schemas.GatewayRequest{row("error", 0, 30, 0), {Kind: "message", Status: "error"}}},
		ArmResult{},
	)
	if r.FailoverWorked || r.CostChangeUSD != nil || !strings.HasPrefix(r.Summary, "Failover did not work") {
		t.Fatalf("report %+v", r)
	}
	partial := BuildReport(ArmResult{}, ArmResult{Requests: []schemas.GatewayRequest{row("success", 1, 1, 0), row("error", 0, 1, 0)}}, ArmResult{})
	if partial.FailoverWorked || !strings.HasPrefix(partial.Summary, "Failover was partial") {
		t.Fatal(partial.Summary)
	}
	none := BuildReport(ArmResult{}, ArmResult{}, ArmResult{})
	if !strings.HasPrefix(none.Summary, "Failover not tested") {
		t.Fatal(none.Summary)
	}
}

func TestBuildRunReport(t *testing.T) {
	r := BuildRunReport(
		RunCell{Runs: 2, Successes: 2, WallS: 20, WallN: 2, CostUSD: 0.02, CostN: 2},
		RunCell{Runs: 2, Successes: 2, FallbackRuns: 2, WallS: 30, WallN: 2, CostUSD: 0.01, CostN: 2},
		RunCell{Runs: 2, Successes: 0, WallS: 4, WallN: 2},
	)
	if !r.FailoverWorked || !r.PerRun || !r.SuccessWith || r.SuccessWithout || r.ExtraLatencyMS != 5000 {
		t.Fatalf("report %+v", r)
	}
	if r.CostChangeUSD == nil || *r.CostChangeUSD > -0.0049 || *r.CostChangeUSD < -0.0051 {
		t.Fatalf("cost %v", r.CostChangeUSD)
	}
	fail := BuildRunReport(RunCell{Runs: 1, Successes: 1}, RunCell{Runs: 2, FallbackRuns: 1}, RunCell{Runs: 1})
	if fail.FailoverWorked || !strings.Contains(fail.Summary, "still failed there") {
		t.Fatalf("report %+v", fail)
	}
}
