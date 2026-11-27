package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/agents"
	agenttemplates "github.com/maximhq/bifrost/bench/agents/templates"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/examples/frameworks"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Agent builder (web parity L08): a template catalog, "create an agent from a
// template" and a form plus YAML editor for one agents/<id>.yaml. Every write
// goes through the org's projectfs store (confined, size-limited) after the
// planner's schema-checked decoder accepts the YAML.

// Template categories, in the order the wizard groups them.
const (
	TemplateCategoryCLI       = "cli"
	TemplateCategoryFramework = "framework"
	TemplateCategoryStarter   = "starter"
	TemplateCategoryBYO       = "bring-your-own"
)

// TemplateParam is one extra field a template asks for (a URL, an image).
type TemplateParam struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Help        string `json:"help,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Required    bool   `json:"required"`
	// Kind is how the form shows it (J3): text (default), secret (a
	// Settings > Secrets name), bool ("true" or "false"), number, select
	// (one of Options), multiline (KEY=VALUE lines). Default pre-fills it.
	Kind    string   `json:"kind,omitempty"`
	Options []string `json:"options,omitempty"`
	Default string   `json:"default,omitempty"`
	// Group puts advanced fields under a fold: "auth", "tools", "advanced",
	// "mapping".
	Group string `json:"group,omitempty"`
	// OptionLabels are display names of a select's options (lane Y10).
	OptionLabels map[string]string `json:"option_labels,omitempty"`
	// ShowWhen shows the field only while each named param has one of the
	// listed values (lane Y10: a preset's inputs, a sign-in method's
	// fields). Hidden fields are still sent.
	ShowWhen map[string][]string `json:"show_when,omitempty"`
}

// AgentTemplate is one entry of GET /agents/templates.
type AgentTemplate struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Category     string          `json:"category"`
	Description  string          `json:"description"`
	Adapter      string          `json:"adapter"`
	Providers    []string        `json:"providers"` // gateway providers the template runs on; first is the default
	DefaultModel string          `json:"default_model"`
	Builds       bool            `json:"builds"` // an image is built from agents/images/<id>/
	Params       []TemplateParam `json:"params"`
	Docs         string          `json:"docs,omitempty"`

	source   string // "repo:<agents yaml stem>", "skeleton:<templates name>", "inline"
	multiEnv bool   // rewrite env "<provider>/{{model}}" values when the provider changes
}

// AgentTemplateList is GET /agents/templates.
type AgentTemplateList struct {
	Templates []AgentTemplate `json:"templates"`
}

// AgentModelRef is the model the wizard picked (the ModelPicker value).
type AgentModelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	KeyID    string `json:"key_id,omitempty"`
}

// AgentFromTemplateRequest is POST /agents/from-template.
type AgentFromTemplateRequest struct {
	Template string            `json:"template"`
	ID       string            `json:"id"`
	Model    *AgentModelRef    `json:"model,omitempty"`
	Notes    string            `json:"notes,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	DryRun   bool              `json:"dry_run,omitempty"` // render and validate only, write nothing
}

// AgentFromTemplateResponse lists what was (or would be) written.
type AgentFromTemplateResponse struct {
	ID     string   `json:"id"`
	Path   string   `json:"path"`
	Files  []string `json:"files"`
	YAML   string   `json:"yaml"`
	DryRun bool     `json:"dry_run"`
}

// AgentConfigFields are the form fields of the config editor.
type AgentConfigFields struct {
	ID          string            `json:"id"`
	Adapter     string            `json:"adapter"`
	Image       string            `json:"image,omitempty"`
	Model       string            `json:"model"`
	Provider    string            `json:"provider"`
	WallS       int               `json:"wall_s"`
	CPUs        float64           `json:"cpus"`
	MemGB       float64           `json:"mem_gb"`
	Turns       int               `json:"turns"`
	Argv        []string          `json:"argv,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	RemoteURL   string            `json:"remote_url,omitempty"`
	A2ACardURL  string            `json:"a2a_card_url,omitempty"`
	OpenAIURL   string            `json:"openai_url,omitempty"` // A08: openai.url of an openai_agent
	Description string            `json:"description"`
	// Behavior is what makes two agents on the same model differ: their
	// instructions, memory, retries and limits, and capabilities. Nil (an
	// older client) leaves those keys of the file alone.
	Behavior *AgentBehaviorFields `json:"behavior,omitempty"`
	// Files are the container paths of the agent's files map, sorted: what
	// an improvement run may list as editable (N128). Read only: a PUT
	// ignores them, the files map is edited in the YAML.
	Files []string `json:"files,omitempty"`
}

// AgentBehaviorFields are the Setup form's behavior fields (lane X11). Each
// maps to one key of the agent YAML; an empty value removes the key.
type AgentBehaviorFields struct {
	NotesProjectFile string   `json:"notes_project_file"` // notes.project_file: the instructions file in the repo
	NotesUserFile    string   `json:"notes_user_file"`    // notes.user_file: a host file installed as user instructions
	NotesUserPath    string   `json:"notes_user_path"`    // notes.user_path: where notes.user_file is installed
	MemoryPaths      []string `json:"memory_paths"`       // memory.paths: paths kept between sessions
	MaxRetries       int      `json:"max_retries"`        // budgets.max_retries
	MaxToolCalls     int      `json:"max_tool_calls"`     // budgets.max_tool_calls
	ProviderRetries  int      `json:"provider_retries"`   // gateway.max_retries: model provider retries
	Fallbacks        []string `json:"fallbacks"`          // gateway.fallbacks: models tried when the model fails
	Capabilities     []string `json:"capabilities"`       // capabilities: empty uses the adapter's defaults
}

// AgentConfigDoc is GET/PUT /agents/{id}/config.
type AgentConfigDoc struct {
	ID     string            `json:"id"`
	Path   string            `json:"path"`
	YAML   string            `json:"yaml"`
	SHA256 string            `json:"sha256"`
	Fields AgentConfigFields `json:"fields"`
	// Version and VersionCreated answer a save (PUT): the agent version
	// current after it, and whether the save made it. A change of model or
	// provider alone keeps the version (agentversions.AgentHash).
	Version        int  `json:"version,omitempty"`
	VersionCreated bool `json:"version_created,omitempty"`
}

// AgentConfigPut is the PUT body: yaml replaces the file; fields (when yaml
// is empty) are applied to the current file, keeping its comments.
type AgentConfigPut struct {
	YAML    string             `json:"yaml,omitempty"`
	Fields  *AgentConfigFields `json:"fields,omitempty"`
	IfMatch string             `json:"if_match,omitempty"`
	// CreateOnly refuses the save (409) when the agent already exists, so a
	// new agent never overwrites one with the same name.
	CreateOnly bool `json:"create_only,omitempty"`
}

// AgentConfigValidateRequest is POST /agents/config/validate.
type AgentConfigValidateRequest struct {
	YAML   string             `json:"yaml,omitempty"`
	Fields *AgentConfigFields `json:"fields,omitempty"` // applied to yaml first, so the form can preview its YAML
}

// AgentConfigValidateResponse says whether the YAML decodes, and why not.
type AgentConfigValidateResponse struct {
	OK     bool               `json:"ok"`
	Error  string             `json:"error,omitempty"`
	YAML   string             `json:"yaml"`
	Fields *AgentConfigFields `json:"fields,omitempty"`
}

var abSecretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var agentIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// abProviderNames maps a ModelPicker provider to a Bifrost gateway provider.
var abProviderNames = map[string]string{
	"openai": "openai", "anthropic": "anthropic", "gemini": "gemini", "openrouter": "openrouter",
	"groq": "groq", "sarvam": "sarvam", "openai-compatible": "openai",
}

var abAnyProvider = []string{"openai", "anthropic", "gemini", "openrouter", "groq"}

func repoTemplate(id, name, cat, stem, desc string, providers []string, model string, multi bool) AgentTemplate {
	return AgentTemplate{ID: id, Name: name, Category: cat, Description: desc, Adapter: "command", Providers: providers,
		DefaultModel: model, Builds: true, Params: []TemplateParam{}, source: "repo:" + stem, multiEnv: multi}
}

// agentTemplates is the catalog. Repo templates copy a shipped agents/*.yaml
// and its image folder; skeletons come from `agent init`; inline ones are
// generated from the params.
var agentTemplates = []AgentTemplate{
	repoTemplate("codex", "Codex CLI", TemplateCategoryCLI, "codex", "OpenAI Codex CLI, headless, routed through Bifrost.", []string{"openai"}, "gpt-5.6-terra", false),
	repoTemplate("claude-code", "Claude Code", TemplateCategoryCLI, "claude-code", "Anthropic Claude Code in print mode, routed through Bifrost.", []string{"anthropic"}, "claude-sonnet-4-5", false),
	repoTemplate("mini-swe-agent", "mini-SWE-agent", TemplateCategoryCLI, "mini-swe-agent", "The 100-line SWE-agent loop with litellm, routed through Bifrost.", []string{"openai"}, "gpt-5.6-terra", false),
	repoTemplate("opencode", "opencode", TemplateCategoryCLI, "opencode", "opencode CLI agent; runs on any provider Bifrost serves.", abAnyProvider, "claude-sonnet-4-5", true),
	repoTemplate("gemini-cli", "Gemini CLI", TemplateCategoryCLI, "gemini-cli", "Google Gemini CLI, headless, routed through Bifrost.", []string{"gemini"}, "gemini-2.5-pro", false),
	repoTemplate("langgraph", "LangGraph", TemplateCategoryFramework, "fw-langgraph", "Example: a LangGraph coding agent (StateGraph + ToolNode). To benchmark your own app, use Wrap my existing app.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("crewai", "CrewAI", TemplateCategoryFramework, "fw-crewai", "A CrewAI coding crew with shell and file tools.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("openai-agents", "OpenAI Agents SDK", TemplateCategoryFramework, "fw-openai-agents", "An OpenAI Agents SDK coding agent.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("pydantic-ai", "Pydantic AI", TemplateCategoryFramework, "fw-pydantic-ai", "A Pydantic AI coding agent.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("llamaindex", "LlamaIndex", TemplateCategoryFramework, "fw-llamaindex", "A LlamaIndex function-calling coding agent.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("ai-sdk", "Vercel AI SDK", TemplateCategoryFramework, "fw-ai-sdk", "A Vercel AI SDK (Node) coding agent.", abAnyProvider, "gpt-5.4-mini", true),
	repoTemplate("plain-loop", "Plain loop", TemplateCategoryFramework, "fw-plain-openai", "No framework: a tool-calling loop on the OpenAI SDK.", abAnyProvider, "gpt-5.4-mini", true),
	{ID: "custom-python", Name: "Custom Python", Category: TemplateCategoryStarter, Adapter: "command", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", Builds: true, Params: []TemplateParam{}, source: "skeleton:python", multiEnv: true,
		Description: "A minimal coding agent in one standard-library Python file you edit.", Docs: "docs/adding-an-agent.md"},
	{ID: "command", Name: "Any CLI", Category: TemplateCategoryStarter, Adapter: "command", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", Builds: true, Params: []TemplateParam{}, source: "skeleton:command",
		Description: "Wrap any command-line agent: put it in the Dockerfile and its command in argv.", Docs: "docs/adding-an-agent.md"},
	{ID: "http-service", Name: "Agent server in the container", Category: TemplateCategoryStarter, Adapter: "command", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", Builds: true, Params: []TemplateParam{}, source: "skeleton:http-service", multiEnv: true,
		Description: "Your agent is a web server (FastAPI, LangGraph server): a launcher starts it and posts each task.", Docs: "docs/adding-an-agent.md"},
	{ID: "http-url", Name: "Connect your HTTP endpoint", Category: TemplateCategoryBYO, Adapter: "remote_http", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", source: "inline", Docs: "docs/remote-execution.md",
		Description: "Your deployed agent. Heimdall posts each task to your URL and reads the answer.",
		Params:      connectHTTPParams},
	{ID: "a2a-card", Name: "Connect your A2A agent", Category: TemplateCategoryBYO, Adapter: "a2a", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", source: "inline", Docs: "docs/a2a-agents.md",
		Description: "An A2A agent reached through Bifrost's agent gateway by its agent card.",
		Params:      connectA2AParams},
	{ID: "openai-endpoint", Name: "Connect your OpenAI-compatible endpoint", Category: TemplateCategoryBYO, Adapter: "openai_agent", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", source: "inline", Docs: "docs/openai-agents.md",
		Description: "Your agent behind an OpenAI-style chat/completions URL. Heimdall sends each task as a chat and runs the task's tools for it.",
		Params:      connectOpenAIParams},
	{ID: "wrap-app", Name: "Wrap my existing app", Category: TemplateCategoryBYO, Adapter: "command", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", source: "wrap", multiEnv: true, Docs: "docs/adding-an-agent.md",
		Description: "Your LangGraph, CrewAI, OpenAI Agents SDK or custom app from a git repository or a Docker image. Heimdall starts it next to each task and posts the task to it.",
		Params:      wrapAppParams},
	{ID: "docker-image", Name: "Your Docker image", Category: TemplateCategoryBYO, Adapter: "command", Providers: abAnyProvider,
		DefaultModel: "claude-sonnet-4-5", source: "inline", multiEnv: true, Docs: "docs/adding-an-agent.md",
		Description: "A prebuilt image that runs your agent as a command. Hosted runs need a digest-pinned image (@sha256:) that a server admin approves.",
		Params: []TemplateParam{
			{Name: "image", Label: "Image", Placeholder: "ghcr.io/your-org/agent@sha256:...", Required: true, Help: "Image reference; pin it by digest for hosted runs, then request approval on Settings > Workers."},
			{Name: "command", Label: "Command", Placeholder: "my-agent --task-file {{instruction_file}}", Required: true,
				Help: "Split on spaces. Use {{instruction_file}} or {{instruction}} for the task."},
		}},
}

func findTemplate(id string) (AgentTemplate, bool) {
	for _, t := range agentTemplates {
		if t.ID == id {
			return t, true
		}
	}
	return AgentTemplate{}, false
}

// renderedFile is one file a template writes, relative to the agents area.
type renderedFile struct {
	Rel  string
	Data []byte
}

// renderAgentTemplate renders template t for req. The first file is always
// <id>.yaml.
func renderAgentTemplate(t AgentTemplate, req AgentFromTemplateRequest) ([]renderedFile, error) {
	id := req.ID
	provider, model := t.Providers[0], t.DefaultModel
	if req.Model != nil {
		if req.Model.Provider != "" {
			p, ok := abProviderNames[req.Model.Provider]
			if !ok { // A09: any Bifrost provider or custom OpenAI-compatible name
				if name, _, custom := schemas.ResolveBifrostProvider(req.Model.Provider); !custom || schemas.ValidCustomProviderID(name) {
					p, ok = name, true
				}
			}
			if !ok {
				return nil, fmt.Errorf("unknown provider %q", req.Model.Provider)
			}
			if !slices.Contains(t.Providers, p) {
				return nil, fmt.Errorf("template %s runs only on %s, not %s", t.ID, strings.Join(t.Providers, ", "), p)
			}
			provider = p
		}
		if m := strings.TrimSpace(req.Model.Model); m != "" {
			if strings.ContainsAny(m, " \t\n\"'{}") {
				return nil, fmt.Errorf("model %q has characters a model id cannot hold", m)
			}
			model = m
		}
	}
	tag := "bench/agent-" + id + ":1"
	var files []renderedFile
	var doc []byte
	switch {
	case strings.HasPrefix(t.source, "repo:"):
		stem := strings.TrimPrefix(t.source, "repo:")
		src, err := fs.ReadFile(agents.Files, stem+".yaml")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", t.ID, err)
		}
		dockerfile, err := abYAMLScalar(src, "build", "dockerfile")
		if err != nil {
			return nil, err
		}
		var fsys fs.FS
		var dir string
		switch {
		case strings.HasPrefix(dockerfile, "agents/images/"):
			fsys, dir = agents.Files, path.Dir(strings.TrimPrefix(dockerfile, "agents/"))
		case strings.HasPrefix(dockerfile, "examples/frameworks/"):
			fsys, dir = frameworks.Files, path.Dir(strings.TrimPrefix(dockerfile, "examples/frameworks/"))
		default:
			return nil, fmt.Errorf("template %s: unknown image folder %s", t.ID, dockerfile)
		}
		err = fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(fsys, p)
			if err != nil {
				return err
			}
			files = append(files, renderedFile{Rel: path.Join("images", id, strings.TrimPrefix(p, dir+"/")), Data: b})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("template %s image: %w", t.ID, err)
		}
		doc = src
		doc, err = editAgentYAML(doc, func(root *yaml.Node) {
			abSetPath(root, id, "id")
			abSetPath(root, tag, "image")
			abSetPath(root, "agents/images/"+id+"/Dockerfile", "build", "dockerfile")
			abSetPath(root, tag, "build", "tag")
		})
		if err != nil {
			return nil, err
		}
	case strings.HasPrefix(t.source, "skeleton:"):
		out, err := agenttemplates.Render(strings.TrimPrefix(t.source, "skeleton:"), id, tag)
		if err != nil {
			return nil, err
		}
		for _, f := range out[1:] {
			files = append(files, renderedFile{Rel: strings.TrimPrefix(f.Path, "agents/"), Data: f.Content})
		}
		doc = out[0].Content
	case t.source == "wrap": // J4: wrap my existing app (agentconnect.go)
		var err error
		if doc, files, err = renderWrapApp(id, tag, req.Params); err != nil {
			return nil, err
		}
	default:
		var err error
		if doc, err = inlineAgentYAML(t, id, req.Params); err != nil {
			return nil, err
		}
	}
	doc, err := editAgentYAML(doc, func(root *yaml.Node) {
		abSetPath(root, model, "model")
		abSetPath(root, provider, "gateway", "provider")
		if t.multiEnv {
			rewriteModelEnv(root, provider)
		}
		head := "Created from the " + t.Name + " template in the web agent builder."
		if n := strings.TrimSpace(req.Notes); n != "" {
			head = n + "\n\n" + head
		}
		if root.HeadComment != "" {
			head += "\n\n" + strings.TrimLeft(root.HeadComment, "# ")
		}
		root.HeadComment = head
	})
	if err != nil {
		return nil, err
	}
	return append([]renderedFile{{Rel: id + ".yaml", Data: doc}}, files...), nil
}

// inlineAgentYAML builds the YAML of a bring-your-own template.
func inlineAgentYAML(t AgentTemplate, id string, params map[string]string) ([]byte, error) {
	get := func(name string) (string, error) {
		v := strings.TrimSpace(params[name])
		if v == "" {
			return "", fmt.Errorf("%s is required for the %s template", name, t.Name)
		}
		if len(v) > 2048 || strings.ContainsAny(v, "\n\r") {
			return "", fmt.Errorf("%s must be one line under 2048 characters", name)
		}
		return v, nil
	}
	httpURL := func(name string) (string, error) {
		v, err := get(name)
		if err != nil {
			return "", err
		}
		u, err := url.Parse(v)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("%s must be an http(s) URL, got %q", name, v)
		}
		return v, nil
	}
	cfg := map[string]any{"id": id, "adapter": t.Adapter, "model": t.DefaultModel,
		"budgets": map[string]any{"wall_s": 900, "cpus": 1, "mem_gb": 2},
		"gateway": map[string]any{"provider": t.Providers[0], "max_retries": 0}}
	switch t.ID {
	case "http-url":
		u, fixed := connectPresetURL(params) // Y10: a platform preset (Bedrock, Agentforce, Copilot Studio) sets the URL
		if !fixed {
			var err error
			if u, err = httpURL("url"); err != nil {
				return nil, err
			}
		}
		block, err := connectHTTPBlock(u, params)
		if err != nil {
			return nil, err
		}
		cfg["image"] = "bench/coding-core-base:1"
		cfg["remote"] = block
	case "a2a-card":
		u, err := httpURL("card_url")
		if err != nil {
			return nil, err
		}
		block, err := connectA2ABlock(u, params)
		if err != nil {
			return nil, err
		}
		cfg["image"] = "bench/coding-core-base:1"
		cfg["a2a"] = block
	case "openai-endpoint":
		u, err := httpURL("url")
		if err != nil {
			return nil, err
		}
		timeout, err := connectTimeout(params)
		if err != nil {
			return nil, err
		}
		block := map[string]any{"url": u, "timeout_s": timeout}
		if connectBool(params, "tools", true) == false {
			block["tools"] = schemas.OpenAIToolsOff
		}
		if strings.HasSuffix(strings.TrimRight(u, "/"), "/responses") {
			block["api"] = "responses"
		}
		if m := strings.TrimSpace(params["model"]); m != "" {
			if len(m) > 256 || strings.ContainsAny(m, " \t\n\r\"'{}") {
				return nil, fmt.Errorf("model %q has characters a model name cannot hold", m)
			}
			block["model"] = m
		}
		if k := strings.TrimSpace(params["api_key_secret"]); k != "" {
			if !abSecretNameRe.MatchString(k) {
				return nil, fmt.Errorf("api key secret name %q must be letters, digits and _ (a Settings > Secrets name)", k)
			}
			block["api_key_secret"] = k
		}
		cfg["image"] = "bench/coding-core-base:1"
		cfg["openai"] = block
	case "docker-image":
		img, err := get("image")
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(img, " \t\"'") {
			return nil, fmt.Errorf("image %q is not a docker image reference", img)
		}
		cmd, err := get("command")
		if err != nil {
			return nil, err
		}
		cfg["image"] = img
		cfg["argv"] = strings.Fields(cmd)
		cfg["env"] = map[string]any{
			"OPENAI_BASE_URL": "{{gateway.url}}/openai/v1", "OPENAI_API_KEY": "{{run.key}}",
			"AGENT_MODEL": "{{model}}", "AGENT_SESSION_ID": "{{run.session}}",
		}
	default:
		vp, err := connectVoiceBlock(t.ID, params) // voice platforms (agentconnectvoice.go)
		if err != nil {
			return nil, err
		}
		rtc, err := connectRTCBlock(t.ID, params) // real-time voice agents (agentconnectrtc.go)
		if err != nil {
			return nil, err
		}
		switch {
		case vp != nil:
			cfg["voice_platform"] = vp
		case rtc != nil:
			cfg["voice_rtc"] = rtc
		default:
			return nil, fmt.Errorf("template %s has no inline form", t.ID)
		}
		cfg["image"] = "bench/coding-core-base:1"
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// modelEnvRe matches an env value that names the run's model, optionally
// behind a provider prefix: "{{model}}" or "anthropic/{{model}}".
var modelEnvRe = regexp.MustCompile(`^([a-z0-9-]+/)?\{\{model\}\}$`)

// rewriteModelEnv points model env values at provider: a bare id goes to
// openai, any other provider needs the provider/ prefix.
func rewriteModelEnv(root *yaml.Node, provider string) {
	env := abMapGet(root, "env")
	if env == nil || env.Kind != yaml.MappingNode {
		return
	}
	want := "{{model}}"
	if provider != "openai" {
		want = provider + "/{{model}}"
	}
	for i := 1; i < len(env.Content); i += 2 {
		if v := env.Content[i]; v.Kind == yaml.ScalarNode && modelEnvRe.MatchString(v.Value) {
			v.Value = want
		}
	}
}

// editAgentYAML parses doc, lets f change its root mapping and re-encodes
// it, keeping comments.
func editAgentYAML(doc []byte, f func(root *yaml.Node)) ([]byte, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil {
		return nil, fmt.Errorf("parse agent YAML: %w", err)
	}
	if n.Kind != yaml.DocumentNode || len(n.Content) != 1 || n.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("agent YAML must be a mapping")
	}
	root := n.Content[0]
	if n.HeadComment != "" && root.HeadComment == "" {
		root.HeadComment, n.HeadComment = n.HeadComment, ""
	}
	f(root)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&n); err != nil {
		return nil, fmt.Errorf("encode agent YAML: %w", err)
	}
	return buf.Bytes(), nil
}

func abMapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// abSetPath sets the scalar at keys, creating mappings on the way. An existing
// key that is not a scalar is left alone. An empty value removes the key.
func abSetPath(m *yaml.Node, value string, keys ...string) {
	for i, k := range keys {
		last := i == len(keys)-1
		var child *yaml.Node
		idx := -1
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == k {
				child, idx = m.Content[j+1], j
				break
			}
		}
		if last {
			if value == "" {
				if idx >= 0 {
					m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
				}
				return
			}
			if child == nil {
				m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
				return
			}
			if child.Kind == yaml.ScalarNode {
				child.Value, child.Tag, child.Style = value, "", 0
				if value == "true" || value == "false" || value == "null" || abLooksNumeric(value) {
					child.Style = yaml.DoubleQuotedStyle
				}
			}
			return
		}
		if child == nil {
			if value == "" {
				return
			}
			child = &yaml.Node{Kind: yaml.MappingNode}
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, child)
		}
		if child.Kind != yaml.MappingNode {
			return
		}
		m = child
	}
}

// abSetNumber sets a numeric scalar (budgets); zero removes it.
func abSetNumber(m *yaml.Node, v float64, keys ...string) {
	if v == 0 {
		abSetPath(m, "", keys...)
		return
	}
	s := fmt.Sprintf("%g", v)
	abSetPath(m, s, keys...)
	if n := abLookupPath(m, keys...); n != nil {
		n.Style, n.Tag = 0, "!!"+map[bool]string{true: "int", false: "float"}[v == float64(int64(v))]
	}
}

func abLookupPath(m *yaml.Node, keys ...string) *yaml.Node {
	for _, k := range keys {
		m = abMapGet(m, k)
	}
	return m
}

var abNumericRe = regexp.MustCompile(`^-?[0-9.]+([eE][-+]?[0-9]+)?$`)

func abLooksNumeric(s string) bool { return abNumericRe.MatchString(s) }

// abYAMLScalar reads one scalar of a YAML document.
func abYAMLScalar(doc []byte, keys ...string) (string, error) {
	var n yaml.Node
	if err := yaml.Unmarshal(doc, &n); err != nil || len(n.Content) == 0 {
		return "", fmt.Errorf("parse agent YAML: %v", err)
	}
	v := abLookupPath(n.Content[0], keys...)
	if v == nil || v.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("agent YAML has no %s", strings.Join(keys, "."))
	}
	return v.Value, nil
}

// decodeAgent runs the planner's schema-checked decoder on doc.
func decodeAgent(doc []byte, rel string) (*schemas.AgentConfig, error) {
	var cfg schemas.AgentConfig
	if err := suite.DecodeYAML(doc, rel, suite.SchemaAgent, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// abFieldsOf turns a decoded config (and its YAML head comment) into form fields.
func abFieldsOf(cfg *schemas.AgentConfig, doc []byte) AgentConfigFields {
	f := AgentConfigFields{ID: cfg.ID, Adapter: cfg.Adapter, Image: cfg.Image, Model: cfg.Model,
		WallS: cfg.Budgets.WallS, CPUs: cfg.Budgets.CPUs, MemGB: cfg.Budgets.MemGB, Turns: cfg.Budgets.Turns, Argv: cfg.Argv,
		Provider: cfg.Gateway.Provider}
	if len(cfg.Env) > 0 {
		f.Env = map[string]string{}
		for k, v := range cfg.Env {
			f.Env[k] = v
		}
	}
	if cfg.Remote != nil {
		f.RemoteURL = cfg.Remote.URL
	}
	if cfg.A2A != nil {
		f.A2ACardURL = cfg.A2A.CardURL
	}
	if cfg.OpenAI != nil {
		f.OpenAIURL = cfg.OpenAI.URL
	}
	f.Behavior = abBehaviorOf(cfg)
	if len(cfg.Files) > 0 {
		f.Files = slices.Sorted(maps.Keys(cfg.Files))
	}
	var n yaml.Node
	if yaml.Unmarshal(doc, &n) == nil && len(n.Content) == 1 {
		f.Description = abStripComment(abHeadComment(&n))
	}
	return f
}

// abHeadComment is the agent's description: the file's head comment. yaml.v3
// splits a head comment with blank lines in it: the paragraphs before the
// last go on the document (or the mapping), the one directly above the first
// key on that key. All of them are read, in that order, so a save (which
// clears every one, abSetHeadComment) keeps the whole comment.
func abHeadComment(doc *yaml.Node) string {
	parts := []string{doc.HeadComment}
	if len(doc.Content) == 1 {
		parts = append(parts, abMappingHeadComment(doc.Content[0]))
	}
	return abJoinComments(parts...)
}

func abMappingHeadComment(root *yaml.Node) string {
	parts := []string{root.HeadComment}
	if root.Kind == yaml.MappingNode && len(root.Content) > 0 {
		parts = append(parts, root.Content[0].HeadComment)
	}
	return abJoinComments(parts...)
}

// abJoinComments joins the non-empty comment parts with a blank comment line.
func abJoinComments(parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

// abSetHeadComment replaces the description on the mapping root: every
// place abHeadComment reads is cleared first, so a save never stacks a
// second comment. editAgentYAML has already moved a document comment onto
// the mapping.
func abSetHeadComment(root *yaml.Node, text string) {
	root.HeadComment = ""
	if root.Kind == yaml.MappingNode && len(root.Content) > 0 {
		root.Content[0].HeadComment = ""
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "# " + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "#"))
	}
	root.HeadComment = strings.Join(lines, "\n")
}

func abStripComment(c string) string {
	lines := strings.Split(c, "\n")
	for i, l := range lines {
		l = strings.TrimPrefix(l, "#")
		lines[i] = strings.TrimPrefix(l, " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// abApplyFields writes the form fields onto doc, keeping everything else.
// The id is never changed here: renaming is a new agent.
func abApplyFields(doc []byte, f AgentConfigFields) ([]byte, error) {
	return editAgentYAML(doc, func(root *yaml.Node) {
		abSetPath(root, f.Model, "model")
		abSetPath(root, f.Provider, "gateway", "provider")
		if f.Image != "" {
			abSetPath(root, f.Image, "image")
		}
		abSetNumber(root, float64(f.WallS), "budgets", "wall_s")
		abSetNumber(root, f.CPUs, "budgets", "cpus")
		abSetNumber(root, f.MemGB, "budgets", "mem_gb")
		abSetNumber(root, float64(f.Turns), "budgets", "turns")
		if f.RemoteURL != "" {
			abSetPath(root, f.RemoteURL, "remote", "url")
		}
		if f.A2ACardURL != "" {
			abSetPath(root, f.A2ACardURL, "a2a", "card_url")
		}
		if f.OpenAIURL != "" {
			abSetPath(root, f.OpenAIURL, "openai", "url")
		}
		if f.Env != nil {
			env := abMapGet(root, "env")
			if env == nil {
				env = &yaml.Node{Kind: yaml.MappingNode}
				root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "env"}, env)
			}
			if env.Kind == yaml.MappingNode {
				kept := env.Content[:0]
				for i := 0; i+1 < len(env.Content); i += 2 {
					if _, ok := f.Env[env.Content[i].Value]; ok {
						kept = append(kept, env.Content[i], env.Content[i+1])
					}
				}
				env.Content = kept
				keys := make([]string, 0, len(f.Env))
				for k := range f.Env {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				for _, k := range keys {
					abSetPath(env, f.Env[k], k)
				}
			}
		}
		if f.Behavior != nil {
			abApplyBehavior(root, *f.Behavior)
		}
		abSetHeadComment(root, f.Description)
	})
}

// abBehaviorOf reads the behavior fields of a decoded config. Lists are
// never nil so the form always gets arrays.
func abBehaviorOf(cfg *schemas.AgentConfig) *AgentBehaviorFields {
	b := &AgentBehaviorFields{
		NotesProjectFile: cfg.Notes.ProjectFile, NotesUserFile: cfg.Notes.UserFile, NotesUserPath: cfg.Notes.UserPath,
		MemoryPaths: []string{}, MaxRetries: cfg.Budgets.MaxRetries, MaxToolCalls: cfg.Budgets.MaxToolCalls,
		ProviderRetries: cfg.Gateway.MaxRetries, Fallbacks: append([]string{}, cfg.Gateway.Fallbacks...), Capabilities: []string{},
	}
	if cfg.Memory != nil {
		b.MemoryPaths = append(b.MemoryPaths, cfg.Memory.Paths...)
	}
	for _, c := range cfg.Capabilities {
		b.Capabilities = append(b.Capabilities, string(c))
	}
	return b
}

// abApplyBehavior writes the behavior fields onto the YAML root. Mappings
// left empty (notes, memory) are removed so the file stays tidy.
func abApplyBehavior(root *yaml.Node, b AgentBehaviorFields) {
	abSetPath(root, strings.TrimSpace(b.NotesProjectFile), "notes", "project_file")
	abSetPath(root, strings.TrimSpace(b.NotesUserFile), "notes", "user_file")
	abSetPath(root, strings.TrimSpace(b.NotesUserPath), "notes", "user_path")
	abSetList(root, b.MemoryPaths, "memory", "paths")
	abSetNumber(root, float64(b.MaxRetries), "budgets", "max_retries")
	abSetNumber(root, float64(b.MaxToolCalls), "budgets", "max_tool_calls")
	abSetNumber(root, float64(b.ProviderRetries), "gateway", "max_retries")
	abSetList(root, b.Fallbacks, "gateway", "fallbacks")
	abSetList(root, b.Capabilities, "capabilities")
	for _, k := range []string{"notes", "memory"} {
		if n := abMapGet(root, k); n != nil && n.Kind == yaml.MappingNode && len(n.Content) == 0 {
			abSetPath(root, "", k)
		}
	}
}

// abSetList sets a flow list of strings at keys, creating mappings on the
// way. Blank items are dropped; an empty list removes the key.
func abSetList(m *yaml.Node, values []string, keys ...string) {
	var items []*yaml.Node
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			items = append(items, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
		}
	}
	if len(items) == 0 {
		abRemovePath(m, keys...)
		return
	}
	parent := m
	for _, k := range keys[:len(keys)-1] {
		child := abMapGet(parent, k)
		if child == nil {
			child = &yaml.Node{Kind: yaml.MappingNode}
			parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, child)
		}
		if child.Kind != yaml.MappingNode {
			return
		}
		parent = child
	}
	seq := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle, Content: items}
	last := keys[len(keys)-1]
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == last {
			parent.Content[i+1] = seq
			return
		}
	}
	parent.Content = append(parent.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: last}, seq)
}

// abRemovePath removes the key at keys whatever its kind.
func abRemovePath(m *yaml.Node, keys ...string) {
	parent := m
	for _, k := range keys[:len(keys)-1] {
		if parent = abMapGet(parent, k); parent == nil || parent.Kind != yaml.MappingNode {
			return
		}
	}
	last := keys[len(keys)-1]
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == last {
			parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
			return
		}
	}
}

// agentBuilder serves the routes.
type agentBuilder struct {
	mode     schemas.Mode
	projects projectfs.Store
}

func init() {
	RegisterOrgArea("agentbuilder", func(env OrgEnv, r *router.Router) {
		h := &agentBuilder{mode: env.Mode, projects: env.Services.Projects}
		r.GET(APIPrefix+"/agents/templates", env.R(h.templates))
		r.POST(APIPrefix+"/agents/from-template", env.W(h.fromTemplate))
		r.POST(APIPrefix+"/agents/config/validate", env.R(h.validate))
		r.GET(APIPrefix+"/agents/{id}/config", env.R(h.getConfig))
		r.PUT(APIPrefix+"/agents/{id}/config", env.W(h.putConfig))
	})
}

func (h *agentBuilder) templates(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AgentTemplateList{Templates: agentTemplates})
}

func (h *agentBuilder) noStore(ctx *fasthttp.RequestCtx) bool {
	if h.projects == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: start it with a project folder (serve -root) to create and edit agents")
		return true
	}
	return false
}

func (h *agentBuilder) badRequest(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func abDecodeBody(ctx *fasthttp.RequestCtx, out any) error {
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("request body is not valid JSON: %v", err)
	}
	return nil
}

func (h *agentBuilder) storeError(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, projectfs.ErrConflict):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": the file changed since you opened it; reload and apply your edit again")
	case errors.Is(err, projectfs.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, what+": file is over 1 MiB")
	case errors.Is(err, projectfs.ErrInvalid), errors.Is(err, projectfs.ErrOutside):
		h.badRequest(ctx, what+": "+err.Error())
	case errors.Is(err, projectfs.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": not found")
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}

func abActor(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	if p.Email != "" {
		return p.Email
	}
	return p.UserID
}

func (h *agentBuilder) fromTemplate(ctx *fasthttp.RequestCtx) {
	var req AgentFromTemplateRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	h.createFromTemplate(ctx, req)
}

// createFromTemplate renders, validates and writes a new agent from a
// template (POST /agents/from-template and POST /agents, lane Y10).
func (h *agentBuilder) createFromTemplate(ctx *fasthttp.RequestCtx, req AgentFromTemplateRequest) {
	t, ok := findTemplate(req.Template)
	if !ok {
		h.badRequest(ctx, fmt.Sprintf("unknown template %q: GET /agents/templates lists them", req.Template))
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if !agentIDRe.MatchString(req.ID) {
		h.badRequest(ctx, fmt.Sprintf("agent name %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit (max 63)", req.ID))
		return
	}
	if req.ID == "new" || req.ID == "diff" || req.ID == "templates" || req.ID == "config" {
		h.badRequest(ctx, fmt.Sprintf("agent name %q is reserved by the web UI: pick another", req.ID))
		return
	}
	if len(req.Notes) > 4000 {
		h.badRequest(ctx, "notes are limited to 4000 characters")
		return
	}
	if !req.DryRun && h.noStore(ctx) {
		return
	}
	files, err := renderAgentTemplate(t, req)
	if err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	rel := "agents/" + req.ID + ".yaml"
	if _, err := decodeAgent(files[0].Data, rel); err != nil {
		h.badRequest(ctx, "the rendered agent does not validate: "+err.Error())
		return
	}
	out := AgentFromTemplateResponse{ID: req.ID, Path: rel, YAML: string(files[0].Data), DryRun: req.DryRun, Files: make([]string, 0, len(files))}
	for _, f := range files {
		out.Files = append(out.Files, "agents/"+f.Rel)
	}
	if req.DryRun {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	for _, rp := range []string{files[0].Rel, path.Join("images", req.ID)} {
		if _, _, err := h.projects.Read(work, projectfs.AreaAgents, rp); err == nil || !errors.Is(err, projectfs.ErrNotFound) {
			if err == nil || errors.Is(err, projectfs.ErrInvalid) { // ErrInvalid: it is a folder
				lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
					fmt.Sprintf("an agent named %s already exists (agents/%s): pick another name", req.ID, rp))
				return
			}
		}
	}
	opts := projectfs.WriteOpts{Actor: abActor(ctx), Note: "agent builder: from template " + t.ID}
	// Image files first, the YAML last: a half-written agent never shows up in the list.
	for i := len(files) - 1; i >= 0; i-- {
		if _, err := h.projects.Write(work, projectfs.AreaAgents, files[i].Rel, files[i].Data, opts); err != nil {
			h.storeError(ctx, "write agents/"+files[i].Rel, err)
			return
		}
	}
	snapshotAgentSave(work, h.projects, files[0].Rel, req.ID, files[0].Data, opts.Actor, "created from template "+t.ID) // A06: agent versions
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, out)
}

func (h *agentBuilder) agentID(ctx *fasthttp.RequestCtx) (string, bool) {
	id, _ := ctx.UserValue("id").(string)
	if !agentIDRe.MatchString(id) {
		h.badRequest(ctx, fmt.Sprintf("agent id %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit, at most 63 characters", id))
		return "", false
	}
	return id, true
}

func (h *agentBuilder) readDoc(work context.Context, id string) ([]byte, projectfs.Entry, error) {
	return h.projects.Read(work, projectfs.AreaAgents, id+".yaml")
}

func (h *agentBuilder) sendDoc(ctx *fasthttp.RequestCtx, status int, id string, doc []byte, sha string) {
	h.sendDocVersion(ctx, status, AgentConfigDoc{ID: id, Path: "agents/" + id + ".yaml", YAML: string(doc), SHA256: sha})
}

// sendDocVersion sends out with its form fields filled from out.YAML.
func (h *agentBuilder) sendDocVersion(ctx *fasthttp.RequestCtx, status int, out AgentConfigDoc) {
	id, doc := out.ID, []byte(out.YAML)
	if cfg, err := decodeAgent(doc, out.Path); err == nil {
		out.Fields = abFieldsOf(cfg, doc)
	} else {
		out.Fields = AgentConfigFields{ID: id}
	}
	lib.SendJSON(ctx, h.mode, status, out)
}

func (h *agentBuilder) getConfig(ctx *fasthttp.RequestCtx) {
	id, ok := h.agentID(ctx)
	if !ok || h.noStore(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	doc, e, err := h.readDoc(work, id)
	if err != nil {
		h.storeError(ctx, "read agents/"+id+".yaml", err)
		return
	}
	h.sendDoc(ctx, fasthttp.StatusOK, id, doc, e.SHA256)
}

func (h *agentBuilder) putConfig(ctx *fasthttp.RequestCtx) {
	id, ok := h.agentID(ctx)
	if !ok || h.noStore(ctx) {
		return
	}
	var req AgentConfigPut
	if err := abDecodeBody(ctx, &req); err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rel := "agents/" + id + ".yaml"
	var doc []byte
	switch {
	case req.YAML != "":
		doc = []byte(req.YAML)
	case req.Fields != nil:
		cur, e, err := h.readDoc(work, id)
		if err != nil {
			h.storeError(ctx, "read "+rel, err)
			return
		}
		if req.IfMatch == "" {
			req.IfMatch = e.SHA256
		}
		if doc, err = abApplyFields(cur, *req.Fields); err != nil {
			h.badRequest(ctx, err.Error())
			return
		}
	default:
		h.badRequest(ctx, "send yaml or fields")
		return
	}
	cfg, err := decodeAgent(doc, rel)
	if err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	if cfg.ID != id {
		h.badRequest(ctx, fmt.Sprintf("id in the YAML (%s) must stay %s: create a new agent to rename it", cfg.ID, id))
		return
	}
	if err := adapters.CheckSkillsExtra(cfg); err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	if req.CreateOnly {
		if _, _, err := h.readDoc(work, id); err == nil || !errors.Is(err, projectfs.ErrNotFound) {
			if err == nil || errors.Is(err, projectfs.ErrInvalid) { // ErrInvalid: it is a folder
				lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
					fmt.Sprintf("An agent named %s already exists. Pick another name.", id))
				return
			}
			h.storeError(ctx, "read "+rel, err)
			return
		}
	}
	e, err := h.projects.Write(work, projectfs.AreaAgents, id+".yaml", doc,
		projectfs.WriteOpts{Actor: abActor(ctx), Note: "agent builder: edit config", IfMatch: req.IfMatch})
	if err != nil {
		h.storeError(ctx, "write "+rel, err)
		return
	}
	out := AgentConfigDoc{ID: id, Path: rel, YAML: string(doc), SHA256: e.SHA256}
	// A06: agent versions. A failure is logged there; the save itself stands.
	out.Version, out.VersionCreated, _ = snapshotAgentSave(work, h.projects, id+".yaml", id, doc, abActor(ctx), "")
	h.sendDocVersion(ctx, fasthttp.StatusOK, out)
}

func (h *agentBuilder) validate(ctx *fasthttp.RequestCtx) {
	var req AgentConfigValidateRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.badRequest(ctx, err.Error())
		return
	}
	if len(req.YAML) > projectfs.MaxFileBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "YAML is over 1 MiB")
		return
	}
	doc := []byte(req.YAML)
	if req.Fields != nil {
		var err error
		if doc, err = abApplyFields(doc, *req.Fields); err != nil {
			lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AgentConfigValidateResponse{Error: err.Error(), YAML: req.YAML})
			return
		}
	}
	out := AgentConfigValidateResponse{YAML: string(doc)}
	cfg, err := decodeAgent(doc, "agent.yaml")
	if err != nil {
		out.Error = err.Error()
	} else {
		out.OK = true
		f := abFieldsOf(cfg, doc)
		out.Fields = &f
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
