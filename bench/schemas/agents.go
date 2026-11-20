package schemas

import (
	"context"
	"time"
)

// Multi-agent runs (docs/multi-agent.md). A run can hold more than one agent:
// in-process subagents (Claude Code Task tool, Codex sub-threads, OpenAI
// Agents SDK handoffs), or a team of containers the bench starts, one per
// member, each on its own gateway key. AgentTree records who the agents were,
// how they were spawned and how they talked; every step of the timeline names
// the agent it belongs to and how that is known. A single-agent run has no
// tree: Evidence.Agents stays nil and agents.json is not written.

// AgentsFile is the run folder file holding the run's AgentTree. It is
// written only for a run with more than one agent and is sealed with the run.
const AgentsFile = "agents.json"

// AgentTreeVersion is the version of the AgentTree shape and of the
// attribution rules that fill it.
const AgentTreeVersion = "1.0.0"

// Attribution sources: how a step or an agent is known, strongest first.
const (
	// AgentSourceGatewayKey: the model call came in on a virtual key the bench
	// minted for one team member's container. The agent cannot pose as
	// another member without that member's credential.
	AgentSourceGatewayKey = "gateway_key"
	// AgentSourceHeader: the agent set x-heimdall-agent (or the Bifrost
	// logging header x-bf-lh-heimdall-agent, or OTel baggage
	// heimdall.agent) on the model call and the gateway logged it. The
	// gateway recorded it, the agent chose it.
	AgentSourceHeader = "header"
	// AgentSourceTranscript: the agent's own transcript files name the
	// subagent (Claude Code sidechain transcripts, Codex sub-threads).
	AgentSourceTranscript = "transcript"
	// AgentSourceOTLP: OpenTelemetry spans the agent exported (invoke_agent
	// spans with gen_ai.agent.id or gen_ai.agent.name, OpenAI Agents SDK
	// agent and handoff spans).
	AgentSourceOTLP = "otlp"
	// AgentSourceSelfReport: native agent events (subagent_start,
	// subagent_end, handoff) the agent sent through the events channels.
	AgentSourceSelfReport = "self_report"
)

// AgentSources lists the attribution sources strongest first.
var AgentSources = []string{AgentSourceGatewayKey, AgentSourceHeader, AgentSourceTranscript, AgentSourceOTLP, AgentSourceSelfReport}

// Attribution strengths. Each source maps to one (AgentSourceStrength).
const (
	AgentStrengthProven   = "proven"   // gateway_key: a bench-minted credential per container
	AgentStrengthDeclared = "declared" // header: recorded by the gateway, chosen by the agent
	AgentStrengthReported = "reported" // transcript, otlp, self_report: the agent's own record
)

// AgentSourceStrength is the strength of an attribution source; an unknown
// source is reported.
func AgentSourceStrength(source string) string {
	switch source {
	case AgentSourceGatewayKey:
		return AgentStrengthProven
	case AgentSourceHeader:
		return AgentStrengthDeclared
	default:
		return AgentStrengthReported
	}
}

// AgentSourceRank orders sources: 0 is the strongest; an unknown source
// ranks after every known one.
func AgentSourceRank(source string) int {
	for i, s := range AgentSources {
		if s == source {
			return i
		}
	}
	return len(AgentSources)
}

// Run shapes of a multi-agent run (AgentTree.Shape).
const (
	AgentShapeInProcess = "in_process" // subagents inside one agent container
	AgentShapeTeam      = "team"       // one container per team member, started by the bench
)

// Agent roles the metrics read. Any other role string is kept as written.
const (
	AgentRoleLead     = "lead"
	AgentRoleWorker   = "worker"
	AgentRoleRouter   = "router"
	AgentRoleJudge    = "judge"
	AgentRoleReviewer = "reviewer"
	AgentRoleSubagent = "subagent"
)

// OrchestrationRoles are the roles whose cost counts as orchestration in
// orchestration_share: the lead, routers and judges.
var OrchestrationRoles = []string{AgentRoleLead, AgentRoleRouter, AgentRoleJudge}

// Agent edge kinds.
const (
	AgentEdgeSpawn   = "spawn"   // From started To
	AgentEdgeHandoff = "handoff" // From handed control to To
	AgentEdgeMessage = "message" // From sent To a message (mailbox, team inbox)
	AgentEdgeClaim   = "claim"   // From claimed a task (To may be empty)
	AgentEdgeReturn  = "return"  // From returned its result to To
)

// AgentRoot is the agent id of the top-level agent of an in-process run when
// the agent names it nothing else.
const AgentRoot = "main"

// AgentTree is every agent of one run, how they were started and how they
// talked, with how much of the step timeline could be attributed to them.
type AgentTree struct {
	Version  string        `json:"version"`
	Shape    string        `json:"shape"` // in_process | team
	Agents   []AgentNode   `json:"agents"`
	Edges    []AgentEdge   `json:"edges,omitempty"`
	Coverage AgentCoverage `json:"coverage"`
	Notes    []string      `json:"notes,omitempty"` // what could not be read or matched, in plain words
}

// AgentNode is one agent of a run. Source is the strongest source that saw
// it and Strength that source's strength; Sources lists every source that
// saw it. Times are zero when no source recorded them.
type AgentNode struct {
	ID              string    `json:"id"`
	Role            string    `json:"role,omitempty"`
	Name            string    `json:"name,omitempty"`
	Model           string    `json:"model,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	Depth           int       `json:"depth"` // 0 for a root agent
	SpawnToolCallID string    `json:"spawn_tool_call_id,omitempty"`
	SpawnEventID    string    `json:"spawn_event_id,omitempty"`
	Started         time.Time `json:"started,omitzero"`
	Ended           time.Time `json:"ended,omitzero"`
	Exit            string    `json:"exit,omitempty"` // exit reason: completed | error | timeout | exit code n
	ExitCode        *int      `json:"exit_code,omitempty"`
	Source          string    `json:"source"`
	Strength        string    `json:"strength"`
	Sources         []string  `json:"sources,omitempty"`
	KeyID           string    `json:"key_id,omitempty"`     // team member: the virtual key id the bench minted
	Container       string    `json:"container,omitempty"`  // team member: its container name
	BudgetUSD       float64   `json:"budget_usd,omitempty"` // team member: its gateway sub-budget
}

// AgentEdge is one interaction between two agents. PayloadSHA256 and Size
// describe the payload without keeping it (a message body, a handoff input).
type AgentEdge struct {
	Kind          string    `json:"kind"` // spawn | handoff | message | claim | return
	From          string    `json:"from"`
	To            string    `json:"to,omitempty"`
	At            time.Time `json:"at,omitzero"`
	PayloadSHA256 string    `json:"payload_sha256,omitempty"`
	Size          int64     `json:"size,omitempty"`
	Source        string    `json:"source"`
	Strength      string    `json:"strength"`
	Ref           string    `json:"ref,omitempty"` // tool call id, ledger sequence or event id that shows it
}

// AgentCoverage says how much of the step timeline carries an agent id. Steps
// is every timeline row considered (all rows except witness rows linked to a
// counted row, which take that row's agent); Attributed of them have an agent
// id, by source and by strength. Unknown stays unknown: a row no source could
// place has no agent id.
type AgentCoverage struct {
	Steps      int            `json:"steps"`
	Attributed int            `json:"attributed"`
	Unknown    int            `json:"unknown"`
	BySource   map[string]int `json:"by_source,omitempty"`
	ByStrength map[string]int `json:"by_strength,omitempty"`
	Available  []string       `json:"available,omitempty"` // sources that had anything to say in this run
}

// TeamMember is one member of a multi-container team (AgentConfig.Team or
// Task.Team). Each member runs in its own container on the run network with
// its own gateway virtual key. Agent names another agent config (a path
// relative to the bench root, or a config id); empty runs the arm's own
// config with this member's role and env. The planner resolves it into
// Config, which is frozen and hashed with the arm.
type TeamMember struct {
	ID        string            `json:"id" yaml:"id"`               // [a-z0-9][a-z0-9_-]*, unique in the team
	Role      string            `json:"role,omitempty" yaml:"role"` // lead | worker | router | judge | reviewer | ...
	Agent     string            `json:"agent,omitempty" yaml:"agent"`
	Model     string            `json:"model,omitempty" yaml:"model"`           // overrides the member config's model
	BudgetUSD float64           `json:"budget_usd,omitempty" yaml:"budget_usd"` // gateway sub-budget of this member's key
	Env       map[string]string `json:"env,omitempty" yaml:"env"`
	Argv      []string          `json:"argv,omitempty" yaml:"argv"` // overrides the member config's argv
	Config    *AgentConfig      `json:"config,omitempty" yaml:"-"`  // resolved by the planner from Agent
}

// MemberKey is one team member's gateway virtual key. Value is a secret and
// is never written to disk.
type MemberKey struct {
	AgentID   string  `json:"agent_id"`
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Value     string  `json:"-"`
	KeySlot   string  `json:"key_slot"` // the member's own fault-proxy key slot, so a probe can target one member
	SlotAuth  string  `json:"-"`
	BudgetUSD float64 `json:"budget_usd,omitempty"`
}

// TeamGateway is the optional gateway extension a team run needs: one
// virtual key per member, each on its own key slot (so a fault plan can
// target one member), under the run-wide budget. FetchRequests on the run
// key then returns every member's rows (RunKey.Members), each with
// GatewayRequest.AgentID set.
type TeamGateway interface {
	MintMemberKeys(ctx context.Context, rc *RunContext, run *RunKey, members []TeamMember) ([]MemberKey, error)
}

// Fairness rules an experiment can declare (Experiment.Fairness).
const (
	// FairnessEqualBudget: every arm has the same total USD cap, enforced at
	// the gateway on the run's key (all agents of the arm, routers and judges
	// included). The planner refuses an experiment whose arms differ or have
	// no cap, and the verdict is INVALID when any run exceeded its cap.
	FairnessEqualBudget = "equal_budget"
)

// Topologies an arm can declare (AgentConfig.Topology), for reports. A
// topology only labels the arm; what runs is the config (Team, subagent
// settings).
const (
	TopologySingle             = "single"
	TopologyBestOfN            = "best_of_n"
	TopologyOrchestratorWorker = "orchestrator_worker"
	TopologyTeam               = "team"
	TopologySwarm              = "swarm"
)

// FairnessReport is the verdict's account of an experiment that declared a
// fairness rule: each arm's cap, which runs exceeded it, and which baselines
// a "multi-agent is better" claim needs were present.
type FairnessReport struct {
	Rule                string             `json:"rule"`
	CapsUSD             map[string]float64 `json:"caps_usd"`              // arm id to its total USD cap
	Topologies          map[string]string  `json:"topologies,omitempty"`  // arm id to its topology
	Exceeded            []string           `json:"exceeded,omitempty"`    // run ids whose cost went over their arm's cap
	SingleAgentBaseline bool               `json:"single_agent_baseline"` // an arm with topology single (or no team) was present
	BestOfNBaseline     bool               `json:"best_of_n_baseline"`    // an arm with topology best_of_n was present
	ClaimAllowed        bool               `json:"claim_allowed"`         // both baselines present and no run exceeded its cap
	Notes               []string           `json:"notes,omitempty"`
}

// AgentLimits are an arm's spawn caps (AgentConfig.Limits), frozen in the
// manifest with the config. Zero means no cap. The over_spawn gate compares
// the run's AgentTree with them.
type AgentLimits struct {
	MaxAgents     int `json:"max_agents,omitempty" yaml:"max_agents"`         // agents in the tree, root included
	MaxDepth      int `json:"max_depth,omitempty" yaml:"max_depth"`           // deepest AgentNode.Depth
	MaxConcurrent int `json:"max_concurrent,omitempty" yaml:"max_concurrent"` // agents running at one time
}

// AttributionSpec opts an experiment in to counterfactual replay of the
// step blamed for a failed run (tier 2 attribution). Stub names what
// replaces the blamed agent's output in the replay.
type AttributionSpec struct {
	Replay     bool   `json:"replay,omitempty" yaml:"replay"`
	MaxReplays int    `json:"max_replays,omitempty" yaml:"max_replays"`
	Stub       string `json:"stub,omitempty" yaml:"stub"`
}

// Mailbox service of a team run (docs/multi-agent.md): an HTTP service the
// bench starts beside every team, reached only through the tool proxy, so
// every message is a ledger row.
const (
	MailboxServer = "mailbox"        // service id: http://toolproxy:9200/s/mailbox/
	MailboxSend   = "POST /send"     // ToolCall.Tool of a send
	MailboxRecv   = "POST /recv"     // ToolCall.Tool of a receive
	MessagesFile  = "messages.jsonl" // the run's AgentMessage records, sealed
	MailboxExport = "messages.jsonl" // the mailbox service's own record, state/mailbox/messages.jsonl
)

// AgentMessage is one message between team members, derived at collect from
// the tool proxy ledger (the send row as the proxy saw it, and the receive
// rows that carried it) and the mailbox service's own export. Body is what
// the sender sent; Received is what the reader actually got when it differs
// (a poisoned or altered delivery). Fields are the send body's keys other
// than from, to and body (a covert or unmonitored channel shows here).
// SenderAgentID is the member whose route the send came in on, when the
// proxy knew it; From is what the sender declared.
type AgentMessage struct {
	Seq           int               `json:"seq"` // 1-based order of sends in the run
	ID            string            `json:"id,omitempty"`
	From          string            `json:"from"`
	To            string            `json:"to"` // a member id, or * for every member
	Body          string            `json:"body"`
	BodySHA256    string            `json:"body_sha256"`
	Size          int64             `json:"size"`
	Fields        map[string]string `json:"fields,omitempty"`
	SenderAgentID string            `json:"sender_agent_id,omitempty"`
	At            time.Time         `json:"at"`
	Delivered     bool              `json:"delivered"`
	DeliveredTo   []string          `json:"delivered_to,omitempty"`
	DeliveredAt   time.Time         `json:"delivered_at,omitzero"`
	Received      string            `json:"received,omitempty"`
	Altered       bool              `json:"altered,omitempty"`
	// ReceivedBy is what each reader got, for the readers whose text
	// differs from Body only: a fault can alter one recipient's copy of a
	// broadcast. Received is the earliest such reader's text.
	ReceivedBy map[string]string `json:"received_by,omitempty"`
	Fault      string            `json:"fault,omitempty"` // a tool fault that answered or changed the send or its delivery
	Nonce      string            `json:"nonce,omitempty"`
	SendSeq    int               `json:"send_seq,omitempty"` // ToolCall.Seq of the send row
	RecvSeqs   []int             `json:"recv_seqs,omitempty"`
}

// TeamEnvironment is the optional environment extension that runs a team:
// one container per member, all started together on the run network with
// the shared workspace volume, each with its own plan and gateway key. It
// returns one result per member, in member order, once every member has
// exited or hit the run's wall budget. A member that could not start has a
// nil Exec and its error in Error; the run then ends as infra_error.
type TeamEnvironment interface {
	ExecTeam(ctx context.Context, rc *RunContext, members []MemberRun) ([]MemberResult, error)
}

// MemberRun is one team member ready to start: the plan its adapter built
// (with its own key in the env), its key and the tool proxy route token
// that stamps its tool calls (ToolCall.AgentID).
type MemberRun struct {
	Member     TeamMember `json:"member"`
	Plan       *RunPlan   `json:"plan"`
	Key        MemberKey  `json:"key"`
	RouteToken string     `json:"-"`
}

// MemberResult is how one team member's container ended. Stream and stderr
// are in the run folder as agent.<member id>.stream.jsonl and
// agent.<member id>.stderr.txt.
type MemberResult struct {
	AgentID   string      `json:"agent_id"`
	Container string      `json:"container,omitempty"`
	Exec      *ExecResult `json:"exec,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// TeamFile is the run folder file holding the team's members, keys (ids,
// never values) and results.
const TeamFile = "team.json"

// TeamRecord is team.json: the members as resolved, their key ids and slots,
// and how each container ended.
type TeamRecord struct {
	Members []TeamMember   `json:"members"`
	Keys    []MemberKey    `json:"keys,omitempty"`
	Results []MemberResult `json:"results,omitempty"`
}
