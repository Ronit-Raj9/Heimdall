package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// promptGateway is the engine's fake gateway that holds run prompt copies.
type promptGateway struct {
	fakeGateway
	fail    error
	created []string
	removed []string
}

func (g *promptGateway) CreateRunPrompt(_ context.Context, runID string, _ schemas.BifrostPrompt) (string, int, error) {
	if g.fail != nil {
		return "", 0, g.fail
	}
	g.created = append(g.created, runID)
	return "p-" + runID, 1, nil
}

func (g *promptGateway) RemoveRunPrompt(_ context.Context, runID string) error {
	g.removed = append(g.removed, runID)
	return nil
}

func testPrompt(t *testing.T) *schemas.BifrostPrompt {
	t.Helper()
	p := &schemas.BifrostPrompt{Name: "support", Version: 3, Messages: []map[string]any{{"role": "system", "content": "Be brief."}}}
	h, err := utils.HashJSON(map[string]any{"messages": p.Messages, "model_params": p.ModelParams})
	if err != nil {
		t.Fatal(err)
	}
	p.SHA256 = h
	return p
}

func promptRunState(t *testing.T) *runState {
	cfg := bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{Prompt: testPrompt(t)})
	return &runState{rc: &schemas.RunContext{Agent: cfg, Run: &schemas.Run{RunID: "r_1"}}}
}

func TestServePromptSetsGatewayHeaders(t *testing.T) {
	gw := &promptGateway{}
	p := &Pipeline{reg: &schemas.Registry{Gateway: gw}}
	st := promptRunState(t)
	if err := p.serveBifrostPrompt(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	if got := st.rc.GatewayHeaders["x-bf-prompt-id"]; got != "p-r_1" {
		t.Fatalf("x-bf-prompt-id = %q", got)
	}
	if got := st.rc.GatewayHeaders["x-bf-prompt-version"]; got != "1" {
		t.Fatalf("x-bf-prompt-version = %q", got)
	}
	plain := &runState{rc: &schemas.RunContext{Agent: bifrostAgent("", nil), Run: &schemas.Run{RunID: "r_2"}}}
	if err := p.serveBifrostPrompt(context.Background(), plain); err != nil || plain.rc.GatewayHeaders != nil || len(gw.created) != 1 {
		t.Fatalf("an arm without a prompt must be a no-op: err %v headers %v created %v", err, plain.rc.GatewayHeaders, gw.created)
	}
}

func TestServePromptFailureFailsPrepare(t *testing.T) {
	p := &Pipeline{reg: &schemas.Registry{Gateway: &promptGateway{fail: errors.New("boom")}}}
	st := promptRunState(t)
	if err := p.serveBifrostPrompt(context.Background(), st); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want the gateway failure", err)
	}
	p = &Pipeline{reg: &schemas.Registry{Gateway: &fakeGateway{}}}
	if err := p.serveBifrostPrompt(context.Background(), promptRunState(t)); err == nil {
		t.Fatal("a gateway that cannot hold prompts must fail prepare")
	}
}

func TestRemovePromptIdempotent(t *testing.T) {
	gw := &promptGateway{}
	p := &Pipeline{reg: &schemas.Registry{Gateway: gw}}
	st := promptRunState(t)
	for i := 0; i < 2; i++ {
		if err := p.removeBifrostPrompt(context.Background(), st); err != nil {
			t.Fatal(err)
		}
	}
	if len(gw.removed) != 2 {
		t.Fatalf("removed = %v", gw.removed)
	}
	if err := p.removeBifrostPrompt(context.Background(), &runState{rc: &schemas.RunContext{}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateBifrostRefusesPromptForHeaderlessAgent(t *testing.T) {
	p := &Planner{reg: &schemas.Registry{Gateway: &fakeGateway{}}}
	cfg := bifrostAgent(adapters.ScriptedToy, &schemas.BifrostFeatures{Prompt: testPrompt(t)})
	err := p.validateBifrost(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "this agent does not send extra headers") {
		t.Fatalf("err = %v", err)
	}
	cfg = bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{Prompt: testPrompt(t)})
	cfg.Team = []schemas.TeamMember{{ID: "lead"}}
	if err := p.validateBifrost(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), "team") {
		t.Fatalf("team err = %v", err)
	}
	empty := &schemas.BifrostPrompt{Name: "x", Version: 1}
	cfg = bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{Prompt: empty})
	if err := p.validateBifrost(context.Background(), cfg, nil); err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("empty err = %v", err)
	}
}

func TestValidateBifrostRefusesChangedPromptCopy(t *testing.T) {
	p := &Planner{reg: &schemas.Registry{Gateway: &fakeGateway{}}}
	pr := testPrompt(t)
	pr.Messages[0]["content"] = "Be long."
	cfg := bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{Prompt: pr})
	err := p.validateBifrost(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt: the copy of prompt support v3 was changed after it was picked") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateBifrostAcceptsPromptOnly(t *testing.T) {
	p := &Planner{reg: &schemas.Registry{Gateway: &fakeGateway{}}}
	cfg := bifrostAgent(adapters.ScriptedMCPLoop, &schemas.BifrostFeatures{Prompt: testPrompt(t)})
	if err := p.validateBifrost(context.Background(), cfg, nil); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
}
