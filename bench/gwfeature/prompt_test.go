package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// promptSource serves one prompt with versions 3 and 4 the way Bifrost's
// Prompt Repository API answers.
func promptSource(t *testing.T) func(ctx context.Context, path string, out any) error {
	t.Helper()
	answers := map[string]string{
		"/api/prompt-repo/prompts/pr-9": `{"prompt": {"id": "pr-9", "name": "support"}}`,
		"/api/prompt-repo/prompts/pr-9/versions": `{"versions": [
			{"version_number": 3, "commit_message": "first", "model_params": {"temperature": 0.2},
			 "messages": [{"order_index": 1, "message": {"role": "user", "content": "second"}}, {"order_index": 0, "message": {"role": "system", "content": "first"}}]},
			{"version_number": 4, "commit_message": "shorter", "messages": [{"order_index": 0, "message": {"role": "system", "content": "Be brief."}}]}
		]}`,
	}
	return func(_ context.Context, path string, out any) error {
		a, ok := answers[path]
		if !ok {
			return fmt.Errorf("status 404 for %s", path)
		}
		return json.Unmarshal([]byte(a), out)
	}
}

func runPromptDraft(t *testing.T, env DraftEnv, opts map[string]string) (*schemas.Experiment, []string, error) {
	t.Helper()
	f, ok := Lookup(schemas.GatewayFeaturePromptVersion)
	if !ok {
		t.Fatal("feature prompt_version is not registered")
	}
	return f.Draft(context.Background(), env, DraftRequest{Feature: f.ID, Suite: "s@1", Base: "agents/a.yaml", Model: "m1", Trials: 2, BudgetUSD: 5, Options: opts})
}

func armPrompt(t *testing.T, arm schemas.ArmSpec) schemas.BifrostPrompt {
	t.Helper()
	b, err := json.Marshal(arm.Set["gateway.bifrost.prompt"])
	if err != nil {
		t.Fatal(err)
	}
	var p schemas.BifrostPrompt
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPromptDraftCopiesVersionContent(t *testing.T) {
	exp, notes, err := runPromptDraft(t, DraftEnv{Root: t.TempDir(), SourceGet: promptSource(t)}, map[string]string{"prompt_id": "pr-9", "variant_version": "4", "control_version": "3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 {
		t.Fatal("want draft notes")
	}
	a, b := exp.Arms["A"], exp.Arms["B"]
	if a.Label != "prompt v3" || b.Label != "prompt v4" {
		t.Fatalf("labels %q %q", a.Label, b.Label)
	}
	if a.Set["model"] != "m1" || b.Set["model"] != "m1" {
		t.Fatalf("every arm sets the model: %v %v", a.Set, b.Set)
	}
	pa := armPrompt(t, a)
	if pa.Name != "support" || pa.Version != 3 || pa.SourceID != "pr-9" || len(pa.Messages) != 2 || pa.Messages[0]["content"] != "first" {
		t.Fatalf("control prompt = %+v", pa)
	}
	want, _ := utils.HashJSON(map[string]any{"messages": pa.Messages, "model_params": pa.ModelParams})
	if pa.SHA256 != want {
		t.Fatalf("sha256 %s, want %s", pa.SHA256, want)
	}
	if pb := armPrompt(t, b); pb.Version != 4 || len(pb.Messages) != 1 {
		t.Fatalf("variant prompt = %+v", pb)
	}
	if exp.GatewayTest == nil || exp.GatewayTest.Feature != schemas.GatewayFeaturePromptVersion || exp.GatewayTest.Control != "A" {
		t.Fatalf("gateway test = %+v", exp.GatewayTest)
	}
	none, _, err := runPromptDraft(t, DraftEnv{Root: t.TempDir(), SourceGet: promptSource(t)}, map[string]string{"prompt_id": "pr-9", "variant_version": "4"})
	if err != nil {
		t.Fatal(err)
	}
	if a := none.Arms["A"]; a.Label != "no Bifrost prompt" || a.Set["gateway.bifrost.prompt"] != nil {
		t.Fatalf("control none = %+v", a)
	}
}

func TestPromptDraftRefusesSameVersion(t *testing.T) {
	env := DraftEnv{Root: t.TempDir(), SourceGet: promptSource(t)}
	if _, _, err := runPromptDraft(t, env, map[string]string{"prompt_id": "pr-9", "variant_version": "4", "control_version": "4"}); err == nil {
		t.Fatal("same version twice must be refused")
	}
	if _, _, err := runPromptDraft(t, env, map[string]string{"prompt_id": "pr-9", "variant_version": "7"}); err == nil || !strings.Contains(err.Error(), "7") {
		t.Fatalf("missing version: %v", err)
	}
}

func TestPromptDraftNeedsSource(t *testing.T) {
	_, _, err := runPromptDraft(t, DraftEnv{Root: t.TempDir()}, map[string]string{"prompt_id": "pr-9", "variant_version": "4"})
	if err == nil || err.Error() != "Connect your Bifrost first" {
		t.Fatalf("err = %v", err)
	}
}

// promptInput is a finished prompt test: arm A without a prompt, arm B with
// one, six tasks, B solving every task and A none.
func promptInput(rowFor func(arm, runID string) Row) Input {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"A": {Label: "no Bifrost prompt"},
		"B": {Label: "prompt v4", Config: schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{Prompt: &schemas.BifrostPrompt{Name: "support", Version: 4, SourceID: "pr-9"}}}}},
	}}
	in := Input{ExpID: "e_p", Manifest: m, Test: schemas.GatewayTest{Feature: schemas.GatewayFeaturePromptVersion, Control: "A", Variants: []string{"B"}, Options: map[string]string{"prompt_id": "pr-9", "variant_version": "4", "control_version": "none"}}}
	for i := 0; i < 6; i++ {
		for _, arm := range []string{"A", "B"} {
			ok := arm == "B"
			cost := 0.1
			id := fmt.Sprintf("r_%s%d", arm, i)
			in.Runs = append(in.Runs, Run{RunID: id, Task: fmt.Sprintf("t%d", i), Arm: arm, Success: &ok, CostUSD: &cost, Rows: []Row{rowFor(arm, id)}})
		}
	}
	return in
}

func computePrompt(t *testing.T, in Input) Effect {
	t.Helper()
	f, ok := Lookup(schemas.GatewayFeaturePromptVersion)
	if !ok {
		t.Fatal("feature prompt_version is not registered")
	}
	return f.Compute(context.Background(), in)
}

func TestPromptNotAppliedWhenRowsLackPrompt(t *testing.T) {
	e := computePrompt(t, promptInput(func(arm, id string) Row {
		return Row{Kind: "message", Status: "success", RawKnown: true}
	}))
	if e.State != StateNotApplied || e.Result != "failed" || !strings.Contains(e.Reason, "Bifrost did not apply the prompt on 6 of 6 calls of setup prompt v4") {
		t.Fatalf("effect = %+v", e)
	}
	e = computePrompt(t, promptInput(func(arm, id string) Row {
		return Row{Kind: "message", Status: "success", RawKnown: true, PromptName: "hm-prompt-" + id, PromptVersion: "1"}
	}))
	if e.State != StateNotApplied || !strings.Contains(e.Reason, "setup A got a prompt it did not ask for") {
		t.Fatalf("effect = %+v", e)
	}
}

func TestPromptUnknownWhenRawMissing(t *testing.T) {
	e := computePrompt(t, promptInput(func(arm, id string) Row {
		return Row{Kind: "message", Status: "success"}
	}))
	if e.State != StateUnknown || !strings.Contains(e.Reason, "Bifrost's full call records were kept for only 0 of 12 calls") {
		t.Fatalf("effect = %+v", e)
	}
}

func TestPromptSettingHeaders(t *testing.T) {
	e := computePrompt(t, promptInput(func(arm, id string) Row {
		r := Row{Kind: "message", Status: "success", RawKnown: true}
		if arm == "B" {
			r.PromptName, r.PromptVersion = "hm-prompt-"+id, "1"
		}
		return r
	}))
	if e.State != StateMeasured || e.Tasks != 6 || e.SolveDelta == nil || e.SolveDelta.Point != 1 {
		t.Fatalf("effect = %+v", e)
	}
	if e.Setting == nil || e.Setting.Kind != "request_headers" || e.Setting.Body != "x-bf-prompt-id: pr-9\nx-bf-prompt-version: 4" {
		t.Fatalf("setting = %+v", e.Setting)
	}
	if e.Result != "worked" || !strings.HasPrefix(e.Headline, "Prompt v4 solved 100 points more tasks than no prompt") {
		t.Fatalf("result %q headline %q", e.Result, e.Headline)
	}
}
