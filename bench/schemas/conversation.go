package schemas

// Turn-driven conversations and pull-mode agents (Wave A lane Y11,
// audit/GAP_G1_AGENTS.md P0-2, P0-4, P0-9; docs/conversations.md,
// docs/pull-agents.md).

// Conversation drivers (ConversationSpec.Driver).
const (
	// ConversationDriverAgent is the default: the agent talks to the
	// simulated user itself, through the respond_to_user tool.
	ConversationDriverAgent = "agent"
	// ConversationDriverBench has the bench drive the conversation: the
	// launcher sends the simulated user's message to the agent as a chat
	// turn, gives the agent's reply back to the simulated user and repeats
	// until the user stops or the turn cap is reached. The agent needs no
	// respond_to_user tool.
	ConversationDriverBench = "bench"
)

// Conversation history modes (ConversationSpec.History).
const (
	// ConversationHistoryClient sends the whole conversation so far with
	// every turn (the default), as a stateless chat endpoint expects.
	ConversationHistoryClient = "client"
	// ConversationHistoryServer sends only the new user message and the
	// conversation's session id: the agent keeps its own state.
	ConversationHistoryServer = "server"
)

// DefaultConversationGreeting is the agent's first line when the bench
// drives the conversation and no greeting is set: tau2-bench's default
// first agent message, so imported tau2 tasks open the way they do there.
const DefaultConversationGreeting = "Hi! How can I help you today?"

// MaxConversationTurns caps ConversationSpec.MaxTurns.
const MaxConversationTurns = 100

// ConversationSpec says who drives a conversation with a simulated user
// (a task service with role user, or a tau2 domain server that holds its
// own user). It applies only to tasks that have such a user; every other
// task runs the agent once, as without it.
type ConversationSpec struct {
	Driver   string `json:"driver,omitempty" yaml:"driver"`       // agent (default) | bench
	History  string `json:"history,omitempty" yaml:"history"`     // client (default) | server
	MaxTurns int    `json:"max_turns,omitempty" yaml:"max_turns"` // agent replies; default the simulator's max_turns, else 12
	// Greeting is the agent's opening line, sent to the simulated user
	// before its first message; default DefaultConversationGreeting.
	Greeting string `json:"greeting,omitempty" yaml:"greeting"`
}

// BenchDriven reports whether c asks the bench to drive the conversation.
func (c *ConversationSpec) BenchDriven() bool {
	return c != nil && c.Driver == ConversationDriverBench
}

// Repo modes of a remote coding agent (RemoteAgent.Repo, PullAgent.Repo).
const (
	// RepoTarball sends the agent a signed, expiring URL to a tarball of
	// the task's workspace (the base tree, never the hidden tests) and
	// takes a unified diff back, which is applied to the workspace and
	// graded by the clean-room grader like a local agent's changes.
	RepoTarball = "tarball"
	RepoNone    = "none"
)

// PullAgent is an agent that runs in the customer's own process and
// connects out to Heimdall (heimdall.serve in the Python and TypeScript
// SDKs). Nothing connects in to the customer: the run's launcher parks each
// turn on the server's queue for the agent, and the customer's process
// takes it over outbound HTTPS with an org API token and answers it.
type PullAgent struct {
	// Name is the name heimdall.serve registers under; default the agent id.
	Name string `json:"name,omitempty" yaml:"name"`
	// TimeoutS is the whole run's budget for the agent's answers; default
	// the wall budget.
	TimeoutS int `json:"timeout_s,omitempty" yaml:"timeout_s"`
	// TurnTimeoutS is how long one turn may wait for the customer's process
	// to answer; default 300.
	TurnTimeoutS int `json:"turn_timeout_s,omitempty" yaml:"turn_timeout_s"`
	// Tools is auto (default: the task's MCP tools are offered and every
	// tool call the agent returns is run through the run's tool proxy) or off.
	Tools string `json:"tools,omitempty" yaml:"tools"`
	// MaxToolRounds caps how many rounds of tool calls one turn may make; default 20.
	MaxToolRounds int `json:"max_tool_rounds,omitempty" yaml:"max_tool_rounds"`
	// Repo tarball lets the agent run coding (diff) tasks: repo in, patch out.
	Repo         string            `json:"repo,omitempty" yaml:"repo"` // tarball | none (default)
	Conversation *ConversationSpec `json:"conversation,omitempty" yaml:"conversation"`
}
