// Package platformimport imports an agent that lives on an agent platform
// (Vapi, Retell, ElevenLabs Agents, AWS Bedrock AgentCore, Salesforce
// Agentforce, Microsoft Copilot Studio, Google Vertex AI Agent Engine,
// LangGraph Platform) by its platform id (INFRA_I2 item 9, section 4.8).
//
// Given the id and the org secrets that unlock the platform's API, Import
// reads the agent's definition through that API (name, prompt, first
// message, tools, language, voice settings, protocol) and returns a draft
// Heimdall agent config wired to the right endpoint preset:
//
//   - voice platforms become a voice_platform block (lane Y12 shape:
//     platform, agent_id, api_key_secret);
//   - the other platforms become a remote_http agent with a request and
//     answer mapping and an auth block (lane Y10 shape: remote.mapping,
//     remote.auth; the preset ids match Y10's remotemap presets where Y10
//     has one).
//
// The config names org secrets only; no secret value is ever part of it.
package platformimport

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Platform ids.
const (
	Vapi          = "vapi"
	Retell        = "retell"
	ElevenLabs    = "elevenlabs"
	AgentCore     = "agentcore"
	Agentforce    = "agentforce"
	CopilotStudio = "copilot_studio"
	Vertex        = "vertex"
	LangGraph     = "langgraph"
)

// Secret roles a platform asks for (Request.Secrets keys).
const (
	SecretAPIKey         = "api_key"
	SecretAccessKey      = "access_key"
	SecretSecretKey      = "secret_key"
	SecretSessionToken   = "session_token"
	SecretClientID       = "client_id"
	SecretClientSecret   = "client_secret"
	SecretDirectLine     = "directline_secret"
	SecretServiceAccount = "service_account"
)

// Input is one plain value a platform asks for besides the agent id.
type Input struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// SecretSlot is one org secret a platform asks for.
type SecretSlot struct {
	Role     string `json:"role"`
	Label    string `json:"label"`
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Suggested is the secret name the form offers when the org has none.
	Suggested string `json:"suggested"`
}

// Platform is one importable platform.
type Platform struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Kind is voice (a voice_platform agent) or endpoint (remote_http).
	Kind          string       `json:"kind"`
	Help          string       `json:"help"`
	IDLabel       string       `json:"id_label"`
	IDPlaceholder string       `json:"id_placeholder"`
	Inputs        []Input      `json:"inputs,omitempty"`
	Secrets       []SecretSlot `json:"secrets"`
	// Imports lists what the import reads from the platform.
	Imports []string `json:"imports"`
	Docs    string   `json:"docs"`
}

// Kinds of Platform.
const (
	KindVoice    = "voice"
	KindEndpoint = "endpoint"
)

// Platforms are the importable platforms, in the form's order.
var Platforms = []Platform{
	{ID: Vapi, Label: "Vapi", Kind: KindVoice,
		Help:    "A Vapi assistant. Heimdall reads it with your private key and runs it through Vapi's chat and call APIs.",
		IDLabel: "Assistant id", IDPlaceholder: "5f1c0a9e-2b7d-4c1e-9a43-0d6c7e2f9b11",
		Secrets: []SecretSlot{{Role: SecretAPIKey, Label: "Vapi private key", Required: true, Suggested: "VAPI_API_KEY",
			Help: "Dashboard > Organization > API keys > private key."}},
		Imports: []string{"name", "system prompt", "first message", "tools", "model", "voice", "transcriber language"},
		Docs:    "https://docs.vapi.ai/api-reference/assistants/get"},
	{ID: Retell, Label: "Retell", Kind: KindVoice,
		Help:    "A Retell chat agent (or voice agent). Heimdall reads the agent and its Retell LLM and runs it through Retell's chat API.",
		IDLabel: "Agent id", IDPlaceholder: "agent_3f0e1b2c4d5a6978",
		Secrets: []SecretSlot{{Role: SecretAPIKey, Label: "Retell API key", Required: true, Suggested: "RETELL_API_KEY"}},
		Imports: []string{"name", "general prompt", "begin message", "tools", "model", "voice", "language"},
		Docs:    "https://docs.retellai.com/api-references/get-agent"},
	{ID: ElevenLabs, Label: "ElevenLabs Agents", Kind: KindVoice,
		Help:    "An ElevenLabs conversational agent. Heimdall reads it with your API key and talks to it over the conversation WebSocket.",
		IDLabel: "Agent id", IDPlaceholder: "agent_01jx2y3z4a5b6c7d8e9f",
		Secrets: []SecretSlot{{Role: SecretAPIKey, Label: "ElevenLabs API key", Required: true, Suggested: "ELEVENLABS_API_KEY",
			Help: "Needs the convai read permission."}},
		Imports: []string{"name", "system prompt", "first message", "tools", "model", "voice and its settings", "language"},
		Docs:    "https://elevenlabs.io/docs/api-reference/agents/get"},
	{ID: AgentCore, Label: "AWS Bedrock AgentCore", Kind: KindEndpoint,
		Help:    "An AgentCore Runtime agent. Heimdall reads the runtime with GetAgentRuntime and calls InvokeAgentRuntime, signing each request with SigV4.",
		IDLabel: "Runtime ARN or id", IDPlaceholder: "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/support_agent-AbCdEf1234",
		Inputs: []Input{{Name: "region", Label: "Region", Placeholder: "us-east-1", Help: "Read from the ARN when you paste one."},
			{Name: "qualifier", Label: "Endpoint qualifier", Placeholder: "DEFAULT", Help: "The runtime endpoint to call; DEFAULT when empty."}},
		Secrets: []SecretSlot{
			{Role: SecretAccessKey, Label: "AWS access key id", Required: true, Suggested: "AGENTCORE_ACCESS_KEY_ID",
				Help: "The IAM user needs bedrock-agentcore:GetAgentRuntime and bedrock-agentcore:InvokeAgentRuntime."},
			{Role: SecretSecretKey, Label: "AWS secret access key", Required: true, Suggested: "AGENTCORE_SECRET_ACCESS_KEY"},
			{Role: SecretSessionToken, Label: "AWS session token", Suggested: "AGENTCORE_SESSION_TOKEN", Help: "Only for temporary credentials."}},
		Imports: []string{"name", "description", "protocol", "status", "version"},
		Docs:    "https://docs.aws.amazon.com/bedrock-agentcore-control/latest/APIReference/API_GetAgentRuntime.html"},
	{ID: Agentforce, Label: "Salesforce Agentforce", Kind: KindEndpoint,
		Help:    "An Agentforce agent through the Agent API. Heimdall reads the agent's BotDefinition with your connected app's client credentials, then each run opens a session, sends messages and ends the session.",
		IDLabel: "Agent id", IDPlaceholder: "0XxHn000000x9F1KAI",
		Inputs: []Input{{Name: "my_domain", Label: "My Domain host", Placeholder: "acme.my.salesforce.com", Required: true}},
		Secrets: []SecretSlot{
			{Role: SecretClientID, Label: "Connected app consumer key", Required: true, Suggested: "SALESFORCE_CLIENT_ID"},
			{Role: SecretClientSecret, Label: "Connected app consumer secret", Required: true, Suggested: "SALESFORCE_CLIENT_SECRET",
				Help: "The connected app needs the client credentials flow, the api and chatbot_api scopes and a run-as user."}},
		Imports: []string{"name", "developer name", "description"},
		Docs:    "https://developer.salesforce.com/docs/einstein/genai/guide/agent-api.html"},
	{ID: CopilotStudio, Label: "Microsoft Copilot Studio", Kind: KindEndpoint,
		Help:    "A Copilot Studio agent through Direct Line. Heimdall checks the Direct Line secret and, when you give the Dataverse environment and an Entra app, reads the agent's name and language.",
		IDLabel: "Agent id (bot id) or schema name", IDPlaceholder: "cr4a1_supportAgent",
		Inputs: []Input{
			{Name: "environment_url", Label: "Dataverse environment URL", Placeholder: "https://contoso.crm.dynamics.com",
				Help: "Optional: lets Heimdall read the agent definition."},
			{Name: "tenant_id", Label: "Entra tenant id", Placeholder: "00000000-0000-0000-0000-000000000000", Help: "Needed with the environment URL."}},
		Secrets: []SecretSlot{
			{Role: SecretDirectLine, Label: "Direct Line secret", Required: true, Suggested: "COPILOT_DIRECTLINE_SECRET",
				Help: "Settings > Security > Web channel security in the agent."},
			{Role: SecretClientID, Label: "Entra app client id", Suggested: "ENTRA_CLIENT_ID", Help: "Optional, with the environment URL."},
			{Role: SecretClientSecret, Label: "Entra app client secret", Suggested: "ENTRA_CLIENT_SECRET", Help: "Optional, with the environment URL."}},
		Imports: []string{"name", "schema name", "language"},
		Docs:    "https://learn.microsoft.com/en-us/microsoft-copilot-studio/publication-connect-bot-to-custom-application"},
	{ID: Vertex, Label: "Google Vertex AI Agent Engine", Kind: KindEndpoint,
		Help:    "An agent deployed to Vertex AI Agent Engine (Agent Builder). Heimdall reads the reasoning engine with a service account and calls its query or stream_query method.",
		IDLabel: "Reasoning engine id or resource name", IDPlaceholder: "projects/my-project/locations/us-central1/reasoningEngines/1234567890",
		Inputs: []Input{{Name: "project", Label: "Project id", Placeholder: "my-project", Help: "Read from the resource name or the key when empty."},
			{Name: "region", Label: "Region", Placeholder: "us-central1", Help: "Read from the resource name when you paste one."}},
		Secrets: []SecretSlot{{Role: SecretServiceAccount, Label: "Service account JSON key", Required: true, Suggested: "GCP_SERVICE_ACCOUNT_JSON",
			Help: "Needs roles/aiplatform.user on the project."}},
		Imports: []string{"name", "description", "framework", "methods"},
		Docs:    "https://cloud.google.com/vertex-ai/generative-ai/docs/agent-engine/use/overview"},
	{ID: LangGraph, Label: "LangGraph Platform", Kind: KindEndpoint,
		Help:    "A LangGraph Platform or LangGraph Server deployment. Heimdall reads the assistant (or finds it by graph id) and calls /runs/wait.",
		IDLabel: "Assistant id or graph id", IDPlaceholder: "agent",
		Inputs: []Input{{Name: "deployment_url", Label: "Deployment URL", Placeholder: "https://my-deployment.us.langgraph.app", Required: true}},
		Secrets: []SecretSlot{{Role: SecretAPIKey, Label: "LangSmith API key", Suggested: "LANGSMITH_API_KEY",
			Help: "Sent as x-api-key. Leave empty for a server without auth."}},
		Imports: []string{"name", "graph id", "description", "input fields"},
		Docs:    "https://langchain-ai.github.io/langgraph/cloud/reference/api/api_ref/"},
}

// Find returns a platform by id.
func Find(id string) (Platform, bool) {
	for _, p := range Platforms {
		if p.ID == id {
			return p, true
		}
	}
	return Platform{}, false
}

// IDs lists the platform ids.
func IDs() []string {
	out := make([]string, len(Platforms))
	for i, p := range Platforms {
		out[i] = p.ID
	}
	return out
}

// Request is one import.
type Request struct {
	Platform string            `json:"platform"`
	AgentID  string            `json:"agent_id"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	// Secrets maps a secret role (api_key, access_key, ...) to the name of
	// the org secret that holds it.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// Tool is one tool the platform agent has.
type Tool struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Voice is the voice settings of a voice platform agent.
type Voice struct {
	Provider string         `json:"provider,omitempty"`
	VoiceID  string         `json:"voice_id,omitempty"`
	Model    string         `json:"model,omitempty"`
	Settings map[string]any `json:"settings,omitempty"`
}

// Definition is what the platform knows about the agent.
type Definition struct {
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
	Language     string `json:"language,omitempty"`
	Model        string `json:"model,omitempty"`
	Tools        []Tool `json:"tools,omitempty"`
	Voice        *Voice `json:"voice,omitempty"`
	// Protocol is the agent's serving protocol where the platform says
	// (AgentCore: HTTP, A2A, MCP; Vertex: the framework).
	Protocol string `json:"protocol,omitempty"`
	Status   string `json:"status,omitempty"`
	Version  string `json:"version,omitempty"`
	// Fields holds other values worth showing (graph id, schema name).
	Fields map[string]string `json:"fields,omitempty"`
	// Source is the platform API the definition was read from (no query
	// string, no secret).
	Source string `json:"source"`
}

// Result is a finished import.
type Result struct {
	Platform   string     `json:"platform"`
	AgentID    string     `json:"agent_id"`
	Definition Definition `json:"definition"`
	// Adapter is voice_platform or remote_http.
	Adapter string `json:"adapter"`
	// Preset is the endpoint preset the mapping was made for (Y10 ids:
	// agentforce, copilot_studio, langgraph; new here: agentcore, vertex).
	Preset string `json:"preset,omitempty"`
	// Block is the adapter block of the agent config: voice_platform or
	// remote (url, mapping, auth, auth_secret, auth_header).
	Block map[string]any `json:"block"`
	// CapabilityHints say what the imported agent can do (voice, tools,
	// sessions) so the evaluate wizard can suggest benchmarks.
	CapabilityHints []string `json:"capability_hints"`
	Warnings        []string `json:"warnings"`
	FetchedAt       string   `json:"fetched_at"`
}

// secretNameRe is the org secret name form (Settings > Secrets).
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Check validates a request against its platform before any secret is
// read: known platform, a well formed id, required inputs and secrets.
func Check(req Request) (Platform, error) {
	p, ok := Find(req.Platform)
	if !ok {
		return p, fmt.Errorf("unknown platform %q: want one of %s", req.Platform, strings.Join(IDs(), ", "))
	}
	id := strings.TrimSpace(req.AgentID)
	if id == "" {
		return p, fmt.Errorf("%s needs the %s", p.Label, strings.ToLower(p.IDLabel))
	}
	if len(id) > 512 || strings.ContainsAny(id, " \t\r\n\"'<>\\") {
		return p, fmt.Errorf("%s %q has characters an id cannot hold", strings.ToLower(p.IDLabel), id)
	}
	known := map[string]bool{}
	for _, in := range p.Inputs {
		known[in.Name] = true
		v := strings.TrimSpace(req.Inputs[in.Name])
		if in.Required && v == "" {
			return p, fmt.Errorf("%s needs %s", p.Label, strings.ToLower(in.Label))
		}
		if len(v) > 512 || strings.ContainsAny(v, " \t\r\n\"'<>\\") {
			return p, fmt.Errorf("%s %q has characters it cannot hold", strings.ToLower(in.Label), v)
		}
	}
	for k := range req.Inputs {
		if !known[k] {
			return p, fmt.Errorf("%s takes no input %q", p.Label, k)
		}
	}
	slots := map[string]bool{}
	for _, s := range p.Secrets {
		slots[s.Role] = true
		n := strings.TrimSpace(req.Secrets[s.Role])
		if s.Required && n == "" {
			return p, fmt.Errorf("%s needs the %s: name the org secret that holds it (Settings > Secrets)", p.Label, s.Label)
		}
		if n != "" && !secretNameRe.MatchString(n) {
			return p, fmt.Errorf("secret name %q must be letters, digits and _ (a Settings > Secrets name)", n)
		}
	}
	for k := range req.Secrets {
		if !slots[k] {
			return p, fmt.Errorf("%s takes no secret %q", p.Label, k)
		}
	}
	return p, nil
}

// SecretNames returns the org secret names a request uses, sorted.
func SecretNames(req Request) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range req.Secrets {
		n = strings.TrimSpace(n)
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// importer is one platform's fetch and mapping.
type importer func(ctx context.Context, c *Client, in *job) error

// job is the working state of one import.
type job struct {
	req    Request
	id     string
	values map[string]string // role -> secret value
	res    *Result
}

func (j *job) input(name string) string { return strings.TrimSpace(j.req.Inputs[name]) }

// secretName is the org secret name of a role ("" when not given).
func (j *job) secretName(role string) string { return strings.TrimSpace(j.req.Secrets[role]) }

func (j *job) secret(role string) string { return j.values[role] }

func (j *job) secretValues() []string {
	out := make([]string, 0, len(j.values))
	for _, v := range j.values {
		out = append(out, v)
	}
	return out
}

func (j *job) warn(format string, a ...any) {
	j.res.Warnings = append(j.res.Warnings, fmt.Sprintf(format, a...))
}

func (j *job) hint(h string) {
	for _, x := range j.res.CapabilityHints {
		if x == h {
			return
		}
	}
	j.res.CapabilityHints = append(j.res.CapabilityHints, h)
}

var importers = map[string]importer{
	Vapi:          importVapi,
	Retell:        importRetell,
	ElevenLabs:    importElevenLabs,
	AgentCore:     importAgentCore,
	Agentforce:    importAgentforce,
	CopilotStudio: importCopilotStudio,
	Vertex:        importVertex,
	LangGraph:     importLangGraph,
}

// Import reads the agent's definition from its platform and builds the
// adapter block. secrets maps each org secret name of req.Secrets to its
// value (the caller resolves them from the org's secrets).
func (c *Client) Import(ctx context.Context, req Request, secrets map[string]string) (*Result, error) {
	if _, err := Check(req); err != nil {
		return nil, err
	}
	values := map[string]string{}
	for role, name := range req.Secrets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		v := secrets[name]
		if v == "" {
			return nil, fmt.Errorf("secret %s is not set: add it on Settings > Secrets", name)
		}
		values[role] = strings.TrimSpace(v)
	}
	j := &job{req: req, id: strings.TrimSpace(req.AgentID), values: values,
		res: &Result{Platform: req.Platform, AgentID: strings.TrimSpace(req.AgentID), CapabilityHints: []string{}, Warnings: []string{}}}
	if err := importers[req.Platform](ctx, c, j); err != nil {
		var se *StatusError
		if errors.As(err, &se) {
			cp := *se
			cp.Detail = scrub(cp.Detail, j.secretValues())
			return nil, &cp
		}
		return nil, errors.New(scrub(err.Error(), j.secretValues()))
	}
	j.res.FetchedAt = c.now().UTC().Format(time.RFC3339)
	if j.res.Definition.Name == "" {
		j.res.Definition.Name = j.id
	}
	if len(j.res.Definition.Tools) > 0 {
		j.hint("tools")
	}
	return j.res, nil
}
