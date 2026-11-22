// Package openmodel serves an open model (Qwen, DeepSeek, Llama) on an
// organization's own Modal account so an evaluation can use it like any
// provider (lane Z05, audit/INFRA_I1_RUNTIMES.md 5.9).
//
// The org connects Modal once (Settings, Workers; package sandbox keeps the
// token in the keystore). An admin then picks a model, an engine (vLLM or
// SGLang) and a GPU. Heimdall deploys a Modal app that runs the engine's
// OpenAI-compatible server behind a fresh per-deployment API key, with
// scale to zero (no GPU is billed while no request comes), waits until the
// model answers, and registers the endpoint as one of the org's provider
// keys (provider vllm or sgl, the deployment's https URL as base URL). Runs
// then reach it the usual way: agent, Bifrost, fault proxy (outage and
// stream-cut faults apply), the Modal endpoint. When the evaluation the
// deployment was started for ends, or its time limit passes, Heimdall stops
// the Modal app and deletes the key, so nothing is left running or billed.
//
// Everything runs on the org's account and is billed by Modal to the org;
// Heimdall never uses its own Modal token for an org.
package openmodel

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/sandbox"
)

// Engines.
const (
	EngineVLLM   = "vllm"
	EngineSGLang = "sglang"
)

// Engine versions the deployment installs. Pinned so a deployment today and
// one next month run the same server.
const (
	VLLMVersion   = "0.33.0" // PyPI, 2026-11-02
	SGLangVersion = "0.5.23" // PyPI, 2026-10-30
)

// Provider is the Heimdall provider id an engine's endpoint is registered
// under (schemas.BifrostProviders: vllm, sgl).
func Provider(engine string) string {
	if engine == EngineSGLang {
		return "sgl"
	}
	return "vllm"
}

// Model is one open model the catalog offers.
type Model struct {
	ID          string  `json:"id"` // Hugging Face repo id, also the served model name
	Title       string  `json:"title"`
	Family      string  `json:"family"` // Qwen | DeepSeek | Llama
	ParamsB     float64 `json:"params_b"`
	Licence     string  `json:"licence"`
	MinVRAMMB   int     `json:"min_vram_mb"`   // weights in bf16 plus room for the KV cache at MaxModelLen
	MaxModelLen int     `json:"max_model_len"` // context the server is started with
	ToolCalls   bool    `json:"tool_calls"`    // the server parses tool calls (agents need them)
	Reasoning   bool    `json:"reasoning,omitempty"`
	// Gated models need the org's Hugging Face token (an org secret named
	// HF_TOKEN) to download.
	Gated bool `json:"gated,omitempty"`
	// vLLM and SGLang parser names for tool calls and reasoning.
	vllmTool, sglTool, reasoning string
}

// Catalog is the models Heimdall can serve, smallest first. Licences are the
// model cards' (read 2026-11-22).
var Catalog = []Model{
	{ID: "Qwen/Qwen2.5-0.5B-Instruct", Title: "Qwen2.5 0.5B Instruct", Family: "Qwen", ParamsB: 0.5, Licence: "Apache-2.0",
		MinVRAMMB: 6000, MaxModelLen: 8192, ToolCalls: true, vllmTool: "hermes", sglTool: "qwen25"},
	{ID: "Qwen/Qwen2.5-1.5B-Instruct", Title: "Qwen2.5 1.5B Instruct", Family: "Qwen", ParamsB: 1.5, Licence: "Apache-2.0",
		MinVRAMMB: 8000, MaxModelLen: 16384, ToolCalls: true, vllmTool: "hermes", sglTool: "qwen25"},
	{ID: "deepseek-ai/DeepSeek-R1-Distill-Qwen-1.5B", Title: "DeepSeek R1 Distill Qwen 1.5B", Family: "DeepSeek", ParamsB: 1.5, Licence: "MIT",
		MinVRAMMB: 8000, MaxModelLen: 16384, Reasoning: true, reasoning: "deepseek_r1"},
	{ID: "Qwen/Qwen2.5-7B-Instruct", Title: "Qwen2.5 7B Instruct", Family: "Qwen", ParamsB: 7.6, Licence: "Apache-2.0",
		MinVRAMMB: 22000, MaxModelLen: 16384, ToolCalls: true, vllmTool: "hermes", sglTool: "qwen25"},
	{ID: "Qwen/Qwen2.5-Coder-7B-Instruct", Title: "Qwen2.5 Coder 7B Instruct", Family: "Qwen", ParamsB: 7.6, Licence: "Apache-2.0",
		MinVRAMMB: 22000, MaxModelLen: 16384, ToolCalls: true, vllmTool: "hermes", sglTool: "qwen25"},
	{ID: "deepseek-ai/DeepSeek-R1-Distill-Qwen-7B", Title: "DeepSeek R1 Distill Qwen 7B", Family: "DeepSeek", ParamsB: 7.6, Licence: "MIT",
		MinVRAMMB: 22000, MaxModelLen: 16384, Reasoning: true, reasoning: "deepseek_r1"},
	{ID: "Qwen/Qwen3-8B", Title: "Qwen3 8B", Family: "Qwen", ParamsB: 8.2, Licence: "Apache-2.0",
		MinVRAMMB: 23000, MaxModelLen: 16384, ToolCalls: true, Reasoning: true, vllmTool: "hermes", sglTool: "qwen25", reasoning: "qwen3"},
	{ID: "meta-llama/Llama-3.1-8B-Instruct", Title: "Llama 3.1 8B Instruct", Family: "Llama", ParamsB: 8.0, Licence: "Llama 3.1 Community License",
		MinVRAMMB: 23000, MaxModelLen: 16384, ToolCalls: true, Gated: true, vllmTool: "llama3_json", sglTool: "llama3"},
	{ID: "Qwen/Qwen2.5-32B-Instruct", Title: "Qwen2.5 32B Instruct", Family: "Qwen", ParamsB: 32.8, Licence: "Apache-2.0",
		MinVRAMMB: 78000, MaxModelLen: 16384, ToolCalls: true, vllmTool: "hermes", sglTool: "qwen25"},
	{ID: "meta-llama/Llama-3.3-70B-Instruct", Title: "Llama 3.3 70B Instruct (2 GPUs)", Family: "Llama", ParamsB: 70.6, Licence: "Llama 3.3 Community License",
		MinVRAMMB: 150000, MaxModelLen: 16384, ToolCalls: true, Gated: true, vllmTool: "llama3_json", sglTool: "llama3"},
}

// LookupModel returns the catalog model with id (case-insensitive).
func LookupModel(id string) (Model, bool) {
	for _, m := range Catalog {
		if strings.EqualFold(m.ID, strings.TrimSpace(id)) {
			return m, true
		}
	}
	return Model{}, false
}

// GPUChoice is a GPU the deployment can run on, with how many of them the
// model needs and what an hour of serving costs on the org's Modal bill.
type GPUChoice struct {
	sandbox.GPUType
	Count          int     `json:"count"`
	USDPerHourAll  float64 `json:"usd_per_hour_total"`
	Recommended    bool    `json:"recommended,omitempty"`
	UnsupportedWhy string  `json:"unsupported,omitempty"`
}

// GPUsFor lists every Modal GPU type for m: the count that fits the model
// (1, 2, 4 or 8), the hourly cost, and the cheapest fit from L4 up marked
// recommended. T4 has no bf16; it is offered for models up to 2B parameters
// only (the server runs in fp16 there).
func GPUsFor(m Model) []GPUChoice {
	out := []GPUChoice{}
	best := -1
	for _, g := range sandbox.ModalGPUs {
		c := GPUChoice{GPUType: g}
		for _, n := range []int{1, 2, 4, 8} {
			if g.MemoryMB*n >= m.MinVRAMMB {
				c.Count = n
				break
			}
		}
		switch {
		case c.Count == 0:
			c.UnsupportedWhy = fmt.Sprintf("needs %d MiB; 8 x %s have %d", m.MinVRAMMB, g.Name, g.MemoryMB*8)
		case g.Name == "T4" && m.ParamsB > 2:
			c.UnsupportedWhy = "T4 has no bf16; models over 2B parameters are offered on L4 or larger"
			c.Count = 0
		}
		if c.Count > 0 {
			c.USDPerHourAll = float64(c.Count) * g.USDPerHour
			// T4 (compute 7.5) is offered but never recommended: no bf16,
			// and the engines' fast attention kernels need compute 8.0.
			if g.Name != "T4" && (best < 0 || c.USDPerHourAll < out[best].USDPerHourAll) {
				best = len(out)
			}
		}
		out = append(out, c)
	}
	if best >= 0 {
		out[best].Recommended = true
	}
	return out
}

// CatalogEntry is a model with its GPU choices, as the catalog route
// answers it.
type CatalogEntry struct {
	Model
	GPUs []GPUChoice `json:"gpus"`
}

// CatalogView is GET /open-models/catalog.
type CatalogView struct {
	Models  []CatalogEntry `json:"models"`
	Engines []EngineInfo   `json:"engines"`
}

// EngineInfo is one serving engine.
type EngineInfo struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Version  string `json:"version"`
	Provider string `json:"provider"` // the provider id the endpoint is registered under
	Licence  string `json:"licence"`
}

// View returns the catalog.
func View() CatalogView {
	v := CatalogView{Engines: []EngineInfo{
		{ID: EngineVLLM, Title: "vLLM", Version: VLLMVersion, Provider: Provider(EngineVLLM), Licence: "Apache-2.0"},
		{ID: EngineSGLang, Title: "SGLang", Version: SGLangVersion, Provider: Provider(EngineSGLang), Licence: "Apache-2.0"},
	}}
	for _, m := range Catalog {
		v.Models = append(v.Models, CatalogEntry{Model: m, GPUs: GPUsFor(m)})
	}
	return v
}

// servedName is the model name the server answers to: the catalog id, so
// an agent config names it as vllm/<id> (or sgl/<id>).
func servedName(m Model) string { return m.ID }

// serverArgs are the engine flags beyond model, host, port, key and name.
func serverArgs(engine string, m Model, gpu sandbox.GPUType, count int) []string {
	var a []string
	switch engine {
	case EngineSGLang:
		a = append(a, "--context-length", fmt.Sprint(m.MaxModelLen))
		if count > 1 {
			a = append(a, "--tp", fmt.Sprint(count))
		}
		if m.ToolCalls && m.sglTool != "" {
			a = append(a, "--tool-call-parser", m.sglTool)
		}
		if m.reasoning != "" {
			a = append(a, "--reasoning-parser", m.reasoning)
		}
		if gpu.Name == "T4" {
			a = append(a, "--dtype", "half")
		}
	default:
		a = append(a, "--max-model-len", fmt.Sprint(m.MaxModelLen), "--enforce-eager")
		if count > 1 {
			a = append(a, "--tensor-parallel-size", fmt.Sprint(count))
		}
		if m.ToolCalls && m.vllmTool != "" {
			a = append(a, "--enable-auto-tool-choice", "--tool-call-parser", m.vllmTool)
		}
		if m.reasoning != "" {
			a = append(a, "--reasoning-parser", m.reasoning)
		}
		if gpu.Name == "T4" {
			a = append(a, "--dtype", "half")
		}
	}
	return a
}

var appNameRe = regexp.MustCompile(`^heimdall-om-[a-z0-9]{10}$`)

// AppPrefix starts the name of every Modal app this package deploys.
const AppPrefix = "heimdall-om-"

// appName is the Modal app name of deployment id.
func appName(id string) string { return AppPrefix + id }
