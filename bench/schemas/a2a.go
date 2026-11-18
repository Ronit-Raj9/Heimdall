package schemas

import (
	"context"
	"time"
)

// B1: agents that speak A2A, benchmarked through Bifrost's agent gateway
// (docs/a2a-agents.md). Every field is optional, so a config, a manifest or
// an evidence file without them hashes as before.

// A2A credential modes (A2AAgent.Credential).
const (
	// A2ACredentialForward has the gateway forward the run's key to the
	// agent (registration forward_accepted_credential), so the agent's model
	// and tool calls through Bifrost are on the run key and measured like
	// any agent's. The default.
	A2ACredentialForward = "forward"
	// A2ACredentialNone sends the agent no Bifrost credential. Its model
	// calls, if any, are not seen by this gateway on the run key.
	A2ACredentialNone = "none"
)

// A2AAgent is the a2a adapter's block of an agent config. The agent is a
// deployed A2A server; the bench never runs it. The run's launcher sends the
// task as an A2A message to the agent's endpoint on this bench's Bifrost
// (agent gateway), follows the task to a terminal state and maps the result
// to the task's output kind. Exactly one of CardURL and Registration names
// the agent.
type A2AAgent struct {
	// CardURL is the agent's own agent card URL, as Bifrost reaches it. The
	// bench registers it under a run-scoped name for each run, grants only
	// the run's key, and deletes the registration at cleanup.
	CardURL string `json:"card_url,omitempty" yaml:"card_url"`
	// Registration is an agent already registered in the gateway. The run's
	// key is new for every run, so the registration must allow any virtual
	// key (allow_by_default); the bench never edits it.
	Registration string `json:"registration,omitempty" yaml:"registration"`
	// Streaming sends SendStreamingMessage and follows the event stream;
	// otherwise SendMessage, then GetTask until the task is terminal.
	Streaming bool `json:"streaming,omitempty" yaml:"streaming"`
	TimeoutS  int  `json:"timeout_s,omitempty" yaml:"timeout_s"`
	// Credential is forward (default) or none (A2ACredential*).
	Credential string `json:"credential,omitempty" yaml:"credential"`
	// Tools expose publishes the run's tool proxy to the agent like
	// remote.tools (docs/remote-execution.md), which lets it run state
	// tasks; the URLs travel in the message metadata (heimdall.tools).
	Tools string `json:"tools,omitempty" yaml:"tools"` // expose | off (default)
	// Artifacts inline writes the file parts of the task's artifacts into
	// the workspace, which lets it run artifact tasks.
	Artifacts string `json:"artifacts,omitempty" yaml:"artifacts"` // inline | none (default)
	// Team lists further A2A agents the entry agent may delegate to through
	// the gateway. Each is registered per run, granted the run's key, and
	// sends Bifrost its own member key (runtime auth), so every model call
	// and every delegation it makes is on a key only it holds.
	Team []A2AMember `json:"team,omitempty" yaml:"team"`
}

// A2AMember is one delegate of an A2A team.
type A2AMember struct {
	ID        string  `json:"id" yaml:"id"` // [a-z0-9][a-z0-9-]*, unique in the team; "lead" is the entry agent
	CardURL   string  `json:"card_url" yaml:"card_url"`
	Role      string  `json:"role,omitempty" yaml:"role"`
	BudgetUSD float64 `json:"budget_usd,omitempty" yaml:"budget_usd"`
}

// A2ARun is the run-time state of an a2a run (RunContext.A2A): the
// registrations the launcher and the team use. Never written to disk as is.
type A2ARun struct {
	Entry     string            // registration the launcher calls
	Owned     []string          // registrations the bench created for this run, deleted at cleanup
	Members   map[string]string // registration -> member id ("lead" for the entry agent)
	Since     time.Time
	Problems  []string          // why a registration could not be made, for the evidence
	KeyMember map[string]string // virtual key id -> member id of every key the run minted
	CardURLs  map[string]string // registration -> the card URL it was made from
}

// A2AEvidence is the agent gateway's witness of an a2a run (evidence.json
// "a2a"). Operations come from Bifrost's agent log (GET /api/agents/history,
// table agent_logs), never from the agent.
type A2AEvidence struct {
	Entry         string               `json:"entry"`
	Registrations []A2ARegistration    `json:"registrations,omitempty"`
	Launcher      *A2ALauncherReport   `json:"launcher,omitempty"` // what the bench's launcher sent and received
	Operations    []A2AOperation       `json:"operations,omitempty"`
	StreamEvents  int                  `json:"stream_events,omitempty"` // agent log event rows of the run's streams
	Hops          []A2AHop             `json:"hops,omitempty"`
	Linked        []A2ALinkedRow       `json:"linked,omitempty"`    // LLM and MCP rows whose agent_correlation_id is a task of the run
	Agreement     *A2AAgreement        `json:"agreement,omitempty"` // the launcher's final task against the gateway's record of it
	Complete      bool                 `json:"complete"`            // every operation the launcher made is in the agent log
	Reason        string               `json:"reason,omitempty"`    // why it is incomplete
	Problems      []string             `json:"problems,omitempty"`
	Gateway       string               `json:"gateway,omitempty"` // gateway version the witness came from
	Strength      string               `json:"strength"`          // witnessed (complete), partial or unwitnessed
	Team          map[string]string    `json:"team,omitempty"`    // registration -> member id
	TaskStates    map[string]string    `json:"task_states,omitempty"`
	Fault         *A2AFaultEvidence    `json:"fault,omitempty"`
	Unmatched     []A2ALaunchOperation `json:"unmatched,omitempty"` // launcher operations the agent log does not hold
	Foreign       int                  `json:"foreign,omitempty"`   // request rows of the run's registrations on keys the run did not mint, left out
}

// A2ARegistration is one registration the run used.
type A2ARegistration struct {
	Name    string `json:"name"`
	Member  string `json:"member,omitempty"`
	CardURL string `json:"card_url,omitempty"`
	Owned   bool   `json:"owned"` // created by the bench for this run and deleted at cleanup
}

// A2AOperation is one agent log request row of the run.
type A2AOperation struct {
	ID                  string    `json:"id"`
	RequestID           string    `json:"request_id,omitempty"`
	Timestamp           time.Time `json:"timestamp"`
	Operation           string    `json:"operation"`
	Status              string    `json:"status"`
	Agent               string    `json:"agent"`            // registration name
	Member              string    `json:"member,omitempty"` // member id of the registration
	VirtualKeyID        string    `json:"virtual_key_id,omitempty"`
	Caller              string    `json:"caller,omitempty"` // bench (the launcher) or the member whose key it came in on
	TaskID              string    `json:"task_id,omitempty"`
	ContextID           string    `json:"context_id,omitempty"`
	TaskState           string    `json:"task_state,omitempty"` // from the recorded response or the last stream event
	DownstreamTransport string    `json:"downstream_transport,omitempty"`
	UpstreamTransport   string    `json:"upstream_transport,omitempty"`
	LatencyMS           *float64  `json:"latency_ms,omitempty"`
	UpstreamLatencyMS   *float64  `json:"upstream_latency_ms,omitempty"`
	OverheadLatencyMS   *float64  `json:"overhead_latency_ms,omitempty"`
	Error               string    `json:"error,omitempty"`
	AnswerSHA256        string    `json:"answer_sha256,omitempty"` // of the text parts of the recorded task's artifacts and status message
}

// A2AHop is one agent-to-agent operation through the gateway: an operation
// on a team member's registration that came in on another member's key.
type A2AHop struct {
	From        string `json:"from"`
	To          string `json:"to"`
	OperationID string `json:"operation_id"`
	TaskID      string `json:"task_id,omitempty"`
	Source      string `json:"source"` // gateway_key: proven by the key the operation came in on
}

// A2ALinkedRow is an LLM or MCP log row correlated with a task of the run
// by its agent_correlation_id header.
type A2ALinkedRow struct {
	Kind         string `json:"kind"` // llm | mcp
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	VirtualKeyID string `json:"virtual_key_id,omitempty"`
	Member       string `json:"member,omitempty"`
	OnRunKey     bool   `json:"on_run_key"` // on a key of this run, so in the run's gateway evidence
}

// A2AAgreement compares the launcher's final task with the gateway's.
type A2AAgreement struct {
	TaskID        string `json:"task_id,omitempty"`
	LauncherState string `json:"launcher_state,omitempty"`
	GatewayState  string `json:"gateway_state,omitempty"`
	AnswerMatches *bool  `json:"answer_matches,omitempty"` // the graded answer's hash equals the gateway's recorded one
	Note          string `json:"note,omitempty"`
}

// A2ALauncherReport is the launcher's summary line (a2a_report).
type A2ALauncherReport struct {
	Operations    []A2ALaunchOperation `json:"operations,omitempty"`
	TaskID        string               `json:"task_id,omitempty"`
	ContextID     string               `json:"context_id,omitempty"`
	FinalState    string               `json:"final_state,omitempty"`
	AnswerSHA256  string               `json:"answer_sha256,omitempty"`
	Artifacts     []string             `json:"artifacts,omitempty"`
	StreamEnded   string               `json:"stream_ended,omitempty"` // terminal | early | error
	Recovered     string               `json:"recovered,omitempty"`    // get_task | subscribe, after a stream ended early
	Error         string               `json:"error,omitempty"`
	Polls         int                  `json:"polls,omitempty"`
	ElapsedMS     int64                `json:"elapsed_ms,omitempty"`
	MessageID     string               `json:"message_id,omitempty"`
	ResponseKinds []string             `json:"response_kinds,omitempty"` // task, message, status_update, artifact_update
	Fault         *A2AFaultFired       `json:"fault,omitempty"`          // what the launcher did for the a2a probe
	Notes         []string             `json:"notes,omitempty"`
}

// A2AFaultFired is the launcher's record of the client-side stream fault.
type A2AFaultFired struct {
	Stream      string `json:"stream"`
	AfterEvents int    `json:"after_events,omitempty"`
	DelayMS     int    `json:"delay_ms,omitempty"`
	Fired       bool   `json:"fired"`
}

// A2ALaunchOperation is one operation the launcher sent.
type A2ALaunchOperation struct {
	Method string `json:"method"`
	RPCID  string `json:"rpc_id"`
	TaskID string `json:"task_id,omitempty"`
	Status int    `json:"status"`
	Error  string `json:"error,omitempty"`
}

// A2AFaultEvidence is what a transport recovery probe observed.
type A2AFaultEvidence struct {
	Kind      string `json:"kind"` // interface_failover | stream_drop
	Observed  bool   `json:"observed"`
	Recovered bool   `json:"recovered"`
	Detail    string `json:"detail,omitempty"`
}

// A2AFault is the a2a probe layer's perturbation (ProbeApply.A2A). The
// launcher applies it to its own side of the stream, so it works for any
// A2A agent: drop closes the stream after AfterEvents events, delay stops
// reading for DelayMS after the first event. The task must then be
// recovered through the gateway (GetTask, SubscribeToTask).
type A2AFault struct {
	Stream      string `json:"stream" yaml:"stream"` // drop | delay
	AfterEvents int    `json:"after_events,omitempty" yaml:"after_events"`
	DelayMS     int    `json:"delay_ms,omitempty" yaml:"delay_ms"`
}

// A2AGateway is the optional gateway extension an a2a run needs: agent
// registrations made and removed per run, and the agent log. Bifrost
// implements it (gateways/bifrost/a2a.go).
type A2AGateway interface {
	RegisterA2A(ctx context.Context, r A2ARegistrationSpec) error
	GetA2A(ctx context.Context, name string) (*A2ARegistrationInfo, error)
	DeleteA2A(ctx context.Context, name string) error
	// A2AHistory returns the agent log rows of the named registrations
	// since a time, oldest first, request rows with their recorded
	// response. Rows of other registrations are dropped client-side.
	A2AHistory(ctx context.Context, agents []string, since time.Time) ([]A2ALogRow, error)
	// A2ACorrelated returns the LLM and MCP log rows whose
	// agent_correlation_id is one of the task ids, checked client-side.
	A2ACorrelated(ctx context.Context, taskIDs []string, since time.Time) ([]A2ALinkedRow, error)
}

// A2ARegistrationSpec is one registration the bench makes. RuntimeKey, when
// set, is sent to the agent as its x-bf-vk header (runtime auth) instead of
// forwarding the caller's credential; it is a secret.
type A2ARegistrationSpec struct {
	Name          string
	CardURL       string
	VirtualKeyIDs []string
	Forward       bool
	RuntimeKey    string
}

// A2ARegistrationInfo is what the bench reads of an existing registration.
type A2ARegistrationInfo struct {
	Name           string   `json:"name"`
	CardURL        string   `json:"agent_card_url"`
	Enabled        bool     `json:"enabled"`
	AllowByDefault bool     `json:"allow_by_default"`
	VirtualKeyIDs  []string `json:"virtual_key_ids"`
}

// A2ALogRow is one Bifrost agent log row as the bench reads it.
type A2ALogRow struct {
	ID                  string    `json:"id"`
	Timestamp           time.Time `json:"timestamp"`
	RecordKind          string    `json:"record_kind"`
	Operation           string    `json:"operation"`
	Status              string    `json:"status"`
	AgentName           string    `json:"agent_name"`
	VirtualKeyID        string    `json:"virtual_key_id,omitempty"`
	RequestID           string    `json:"request_id,omitempty"`
	TaskID              string    `json:"task_id,omitempty"`
	ContextID           string    `json:"context_id,omitempty"`
	EventType           string    `json:"event_type,omitempty"`
	TaskState           string    `json:"task_state,omitempty"`
	DownstreamTransport string    `json:"downstream_transport,omitempty"`
	UpstreamTransport   string    `json:"upstream_transport,omitempty"`
	Latency             *float64  `json:"latency,omitempty"`
	UpstreamLatency     *float64  `json:"upstream_latency,omitempty"`
	OverheadLatency     *float64  `json:"overhead_latency,omitempty"`
	ResponseBody        string    `json:"response_body,omitempty"`
	RequestBody         string    `json:"request_body,omitempty"`
	Error               string    `json:"error,omitempty"`
}
