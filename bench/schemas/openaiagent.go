package schemas

// OpenAI-compatible agents (adapter openai_agent, docs/openai-agents.md).

// OpenAI agent API shapes (OpenAIAgent.API).
const (
	OpenAIAPIChat      = "chat"      // POST <url>/chat/completions
	OpenAIAPIResponses = "responses" // POST <url>/responses
)

// OpenAI agent tool modes (OpenAIAgent.Tools).
const (
	OpenAIToolsAuto = "auto" // offer the task's MCP tools as function tools (default)
	OpenAIToolsOff  = "off"  // never offer tools
)

// OpenAIAgent describes a user's agent served as an OpenAI-compatible
// endpoint: chat/completions or the Responses API. The bench never runs it.
// The run's launcher sends the task instruction as a user message (earlier
// sessions of a multi-session task as earlier turns), offers the task's MCP
// tools as function tools, runs each tool call through the run's tool proxy
// and writes the final assistant text as the answer.
type OpenAIAgent struct {
	// URL is the API base (https://agent.example.com/v1) or the full
	// endpoint (.../chat/completions or .../responses).
	URL string `json:"url" yaml:"url"`
	// API is chat (default) or responses.
	API string `json:"api,omitempty" yaml:"api"`
	// Model is the model field sent; default the agent config's model.
	Model string `json:"model,omitempty" yaml:"model"`
	// APIKeySecret names the org secret (Settings > Secrets) sent as
	// "Authorization: Bearer <value>". Empty sends no key.
	APIKeySecret string `json:"api_key_secret,omitempty" yaml:"api_key_secret"`
	// Tools is auto (default) or off.
	Tools string `json:"tools,omitempty" yaml:"tools"`
	// MaxTurns caps the request/tool-call loop; default budgets.turns, else 20.
	MaxTurns int `json:"max_turns,omitempty" yaml:"max_turns"`
	// System is an optional system message sent first.
	System   string `json:"system,omitempty" yaml:"system"`
	TimeoutS int    `json:"timeout_s,omitempty" yaml:"timeout_s"`
}
