package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// prewarmLauncher records the manifests it was asked to prewarm.
type prewarmLauncher struct {
	evalLauncher
	warmed []string
}

func (l *prewarmLauncher) Prewarm(m *schemas.Manifest) { l.warmed = append(l.warmed, m.ExpID) }

func TestEvaluationBareModelBaseline(t *testing.T) {
	l := &prewarmLauncher{}
	r, dir := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	body := `{"mode":"opponent","arms":[{"label":"A","agent":"scripted","model":"gpt-x","provider":"anthropic"},{"label":"B","agent":"bare-model","model":"gpt-x","provider":"anthropic"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":1}`
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	// An org seeded before the baseline existed gets its files on first use.
	for _, rel := range bareModelFiles {
		if _, err := os.Stat(filepath.Join(dir, "agents", rel)); err != nil {
			t.Fatalf("agents/%s not written: %v", rel, err)
		}
	}
	ev := decodeEval(t, ctx)
	exp := l.planned[ev.Children[0].ExperimentID]
	b := exp.Arms["B"]
	if b.Agent != "agents/bare-model.yaml" || b.Set["env.BENCH_MODEL"] != "anthropic/gpt-x" || b.Set["model"] != "gpt-x" {
		t.Fatalf("bare arm %+v", b)
	}
	if len(l.warmed) != 1 || l.warmed[0] != ev.Children[0].ExperimentID {
		t.Fatalf("prewarmed %v", l.warmed)
	}
	// A file the org already has is kept.
	custom := []byte("id: bare-model\nadapter: scripted\n")
	if err := os.WriteFile(filepath.Join(dir, "agents", "bare-model.yaml"), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := evalCall(r, "POST", APIPrefix+"/evaluations", body); got.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("second %d", got.Response.StatusCode())
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "agents", "bare-model.yaml")); string(data) != string(custom) {
		t.Fatalf("overwrote the org's file: %s", data)
	}
}

func TestBareModelArm(t *testing.T) {
	arm := schemas.ArmSpec{Set: map[string]any{"model": "m"}}
	for _, tc := range []struct{ agent, provider, model, want string }{
		{utils.BareModelAgent, "anthropic", "claude-x", "anthropic/claude-x"},
		{utils.BareModelAgent, "", "gpt-x", "openai/gpt-x"},
		{utils.BareModelAgent, "", "openrouter/qwen-3", "openrouter/qwen-3"},
		{utils.BareModelAgent, "openai-compatible", "local", "openai/local"},
		{"codex", "anthropic", "claude-x", ""},
		{utils.BareModelAgent, "anthropic", "", ""},
	} {
		got, _ := bareModelArm(arm, tc.agent, tc.provider, tc.model).Set["env.BENCH_MODEL"].(string)
		if got != tc.want {
			t.Errorf("%s %s %s: %q, want %q", tc.agent, tc.provider, tc.model, got, tc.want)
		}
	}
	if _, ok := arm.Set["env.BENCH_MODEL"]; ok {
		t.Fatal("bareModelArm changed its input")
	}
}

func TestShippedBareModelIsTheBaseline(t *testing.T) {
	h := &evaluationsHandler{}
	if err := h.ensureBareModel(context.Background(), &schemas.EvaluationSpec{Arms: []schemas.EvaluationArm{{Agent: "bare-model"}}}); err != nil {
		t.Fatalf("no project store: %v", err)
	}
	data, err := os.ReadFile(filepath.Join("..", "agents", "bare-model.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg schemas.AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if !utils.IsBareModel(&cfg) || cfg.ID != utils.BareModelAgent {
		t.Fatalf("shipped bare-model config %+v", cfg)
	}
}
