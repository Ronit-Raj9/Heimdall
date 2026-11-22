package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// fakeAgents returns two configs and one file that failed to decode. The
// env values and file contents are secrets that must never be served.
type fakeAgents struct{ err error }

func (f fakeAgents) AgentConfigs(context.Context) ([]AgentConfigFile, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []AgentConfigFile{
		{Path: "agents/bad.yaml", Err: errors.New("agents/bad.yaml: cannot unmarshal !!str `sk-LEAKED-IN-ERROR` into int")},
		{Path: "agents/custom.yaml", Config: &schemas.AgentConfig{
			ID: "custom", Adapter: "command", Image: "bench/agent-custom:1", Model: "claude-sonnet-4-5",
			Build: &schemas.ImageSpec{Dockerfile: "agents/images/custom/Dockerfile", Tag: "bench/agent-custom:1", BaseArg: "SUITE_BASE"},
			Env: map[string]string{
				"BENCH_API_KEY": "{{run.key}}", "OPENAI_API_KEY": "sk-SECRET-VALUE-123", "ZETA_TOKEN": "ghp_SECRETVALUE",
			},
			Files:   map[string]string{"/home/agent/.config/agent.toml": "token = \"FILE-CONTENT-SECRET\"\n"},
			Notes:   schemas.AgentNotes{ProjectFile: "AGENTS.md"},
			Budgets: schemas.Budgets{WallS: 900, CPUs: 1, MemGB: 2, Turns: 40},
			Gateway: schemas.GatewayPolicy{Provider: "anthropic", Fallbacks: []string{"openai"}},
		}},
		{Path: "agents/codex.yaml", Config: &schemas.AgentConfig{
			ID: "codex", Adapter: "command", Image: "bench/agent-codex:1", Model: "gpt-5.6-terra", Parser: "codex",
			Build:   &schemas.ImageSpec{Dockerfile: "agents/images/codex/Dockerfile", Tag: "bench/agent-codex:1"},
			Notes:   schemas.AgentNotes{ProjectFile: "AGENTS.md", UserFile: "notes/user.md"},
			Budgets: schemas.Budgets{WallS: 900, CPUs: 2, MemGB: 4},
			Gateway: schemas.GatewayPolicy{Provider: "openai"},
		}},
		{Path: "agents/noop.yaml", Config: &schemas.AgentConfig{ID: "noop", Adapter: "scripted", Scripted: &schemas.ScriptedSpec{Kind: "noop"}}},
	}, nil
}

// agentByID finds one agent in a decoded list.
func agentByID(t *testing.T, body map[string]any, path string) map[string]any {
	t.Helper()
	for _, a := range objectsAt(t, body, "agents[]") {
		if a["path"] == path {
			return a
		}
	}
	t.Fatalf("agent %s not in %v", path, body)
	return nil
}

func TestAgentsList(t *testing.T) {
	idx := testIndex()
	for i := range idx.runs {
		idx.runs[i].Run.Agent.ConfigID = "custom"
	}
	idx.runs[0].Sealed = false
	resp := newSuitesClient(t, nil, fakeAgents{}, idx).do(t, "GET", APIPrefix+"/agents", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	raw := string(resp.Body())
	for _, secret := range []string{"sk-SECRET-VALUE-123", "ghp_SECRETVALUE", "FILE-CONTENT-SECRET", "{{run.key}}", "sk-LEAKED-IN-ERROR"} {
		if strings.Contains(raw, secret) {
			t.Errorf("response leaked %q: %s", secret, raw)
		}
	}
	body := decodeBody(t, resp)
	custom := agentByID(t, body, "agents/custom.yaml")
	if got := strings.Join(toStrings(custom["env_names"]), ","); got != "BENCH_API_KEY,OPENAI_API_KEY,ZETA_TOKEN" {
		t.Errorf("env_names = %s", got)
	}
	if got := strings.Join(toStrings(custom["file_paths"]), ","); got != "/home/agent/.config/agent.toml" {
		t.Errorf("file_paths = %s", got)
	}
	want := map[string]any{"per_suite_build": true, "base_arg": "SUITE_BASE", "evidence_level": "gateway", "parser": "", "sealed_runs": float64(3)}
	for k, v := range want {
		if custom[k] != v {
			t.Errorf("custom %s = %v, want %v", k, custom[k], v)
		}
	}
	codex := agentByID(t, body, "agents/codex.yaml")
	if codex["evidence_level"] != "native" || codex["per_suite_build"] != false || codex["sealed_runs"] != float64(0) || codex["notes_user_file"] != "notes/user.md" {
		t.Errorf("codex = %v", codex)
	}
	if noop := agentByID(t, body, "agents/noop.yaml"); noop["scripted_kind"] != "noop" || noop["dockerfile"] != "" {
		t.Errorf("noop = %v", noop)
	}
	bad := agentByID(t, body, "agents/bad.yaml")
	if !strings.Contains(bad["error"].(string), "cannot unmarshal") || bad["id"] != "" {
		t.Errorf("bad = %v", bad)
	}
}

func TestAgentsWithoutIndexOrSource(t *testing.T) {
	body := decodeBody(t, newSuitesClient(t, nil, fakeAgents{}, nil).do(t, "GET", APIPrefix+"/agents", nil, nil))
	if a := agentByID(t, body, "agents/codex.yaml"); a["sealed_runs"] != nil {
		t.Errorf("sealed_runs = %v, want null without an index", a["sealed_runs"])
	}
	if resp := newSuitesClient(t, nil, nil, nil).do(t, "GET", APIPrefix+"/agents", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("nil source: status = %d", resp.StatusCode())
	}
	if resp := newSuitesClient(t, nil, fakeAgents{err: errors.New("glob: bad pattern")}, nil).do(t, "GET", APIPrefix+"/agents", nil, nil); resp.StatusCode() != 500 {
		t.Errorf("source error: status = %d", resp.StatusCode())
	}
}

func TestRedactQuoted(t *testing.T) {
	got := redactQuoted("a.yaml: line 3: cannot unmarshal !!str `sk-1` into int; 'x' and \"y\"")
	if got != "a.yaml: line 3: cannot unmarshal !!str [value hidden] into int; [value hidden] and [value hidden]" {
		t.Errorf("redactQuoted = %q", got)
	}
}

// toStrings converts a decoded JSON array of strings.
func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, s := range arr {
		out = append(out, s.(string))
	}
	return out
}

// TestAgentsHiddenControls: with WithHiddenControls the scripted controls
// leave the list; every other config, including a broken file, stays.
func TestAgentsHiddenControls(t *testing.T) {
	for _, hide := range []bool{true, false} {
		ctx := &fasthttp.RequestCtx{}
		NewAgentsHandler(schemas.ModeLive, fakeAgents{}, nil).WithHiddenControls(hide).getAgents(ctx)
		var list schemas.AgentList
		if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, a := range list.Agents {
			paths = append(paths, a.Path)
		}
		want := "agents/bad.yaml,agents/custom.yaml,agents/codex.yaml"
		if !hide {
			want += ",agents/noop.yaml"
		}
		if got := strings.Join(paths, ","); got != want {
			t.Fatalf("hide=%v: agents %s, want %s", hide, got, want)
		}
	}
	if !IsControlAgent(&schemas.AgentConfig{Adapter: "scripted"}) || IsControlAgent(&schemas.AgentConfig{Adapter: "command"}) || IsControlAgent(nil) {
		t.Fatal("IsControlAgent")
	}
}

// TestAgentsHiddenAgents: WithHiddenAgents drops exactly the files it
// reports (the Acme example agents on a production server).
func TestAgentsHiddenAgents(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	NewAgentsHandler(schemas.ModeLive, fakeAgents{}, nil).WithHiddenAgents(func(f AgentConfigFile) bool {
		return f.Path == "agents/custom.yaml"
	}).getAgents(ctx)
	var list schemas.AgentList
	if err := json.Unmarshal(ctx.Response.Body(), &list); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range list.Agents {
		paths = append(paths, a.Path)
	}
	if got := strings.Join(paths, ","); got != "agents/bad.yaml,agents/codex.yaml,agents/noop.yaml" {
		t.Fatalf("agents %s", got)
	}
}
