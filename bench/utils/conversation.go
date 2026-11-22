package utils

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Lane Y11: turn-driven conversations, pull-mode agents and repo in, patch
// out (schemas/conversation.go, docs/conversations.md, docs/pull-agents.md).

// AdapterPull is the adapter name of a pull-mode agent.
const AdapterPull = "pull"

// RespondTool is the simulated user's tool (bench/simuser/simuser.py).
const RespondTool = "respond_to_user"

// pullName is a heimdall.serve agent name.
var pullName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// CheckConversation refuses a conversation block with an unknown driver or
// history, or a turn cap out of range. It is pure; nil is valid.
func CheckConversation(c *schemas.ConversationSpec, block string) error {
	if c == nil {
		return nil
	}
	switch c.Driver {
	case "", schemas.ConversationDriverAgent, schemas.ConversationDriverBench:
	default:
		return fmt.Errorf("%s.conversation.driver %q: want bench or agent", block, c.Driver)
	}
	switch c.History {
	case "", schemas.ConversationHistoryClient, schemas.ConversationHistoryServer:
	default:
		return fmt.Errorf("%s.conversation.history %q: want client or server", block, c.History)
	}
	// The history mode follows the protocol where the protocol fixes it, so
	// the default never skews a stateful agent: an A2A agent keeps the chat
	// in its context (only the new message is sent), an OpenAI-compatible
	// endpoint is stateless (the whole chat is sent). An HTTP endpoint or a
	// pull agent gets the new message and the session id with every turn,
	// and also the whole chat unless history is server, so either kind of
	// agent works on the default.
	switch {
	case block == "a2a" && c.History == schemas.ConversationHistoryClient:
		return fmt.Errorf("a2a.conversation.history client: an A2A agent keeps the conversation in its context, so only history server applies (leave it empty)")
	case block == "openai" && c.History == schemas.ConversationHistoryServer:
		return fmt.Errorf("openai.conversation.history server: an OpenAI-compatible endpoint keeps no state between requests, so the bench sends the whole conversation (leave it empty)")
	}
	if c.MaxTurns < 0 || c.MaxTurns > schemas.MaxConversationTurns {
		return fmt.Errorf("%s.conversation.max_turns %d: want 0 (the simulator's) to %d", block, c.MaxTurns, schemas.MaxConversationTurns)
	}
	if len(c.Greeting) > 2000 {
		return fmt.Errorf("%s.conversation.greeting is longer than 2000 characters", block)
	}
	return nil
}

// AgentConversation is the conversation block of a remote, OpenAI-compatible,
// A2A or pull agent, or nil.
func AgentConversation(a *schemas.AgentConfig) *schemas.ConversationSpec {
	switch {
	case a == nil:
		return nil
	case a.Pull != nil:
		return a.Pull.Conversation
	case a.OpenAI != nil:
		return a.OpenAI.Conversation
	case a.A2A != nil:
		return a.A2A.Conversation
	case a.Remote != nil:
		return a.Remote.Conversation
	}
	return nil
}

// UserServers are the run's simulated users the bench can drive: every
// visible, started MCP service that plays the user (role user, or gateway {}
// like an imported tau2 domain server), as the BENCH_MCP_SERVERS JSON
// shape. Empty when the task has none. It is pure.
func UserServers(services []schemas.ServiceHandle, key string) (map[string]string, string, error) {
	var users []schemas.ServiceHandle
	for _, h := range services {
		if h.Spec.Role == schemas.ServiceRoleUser && h.Spec.MCP == nil && h.URL != "" {
			// A role user service is an MCP server (simuser.py) even when it
			// declares no mcp block.
			h.Spec.Role = schemas.ServiceRoleMCP
		}
		if h.Spec.Role == schemas.ServiceRoleMCP && simulatorBacked(h.Spec) {
			users = append(users, h)
		}
	}
	urls := MCPServers(users)
	if len(urls) == 0 {
		return nil, "", nil
	}
	js, err := MCPServersJSONFor(users, key)
	if err != nil {
		return nil, "", err
	}
	return urls, js, nil
}

// simulatorBacked mirrors simuser.SimulatorBacked (utils cannot import it).
func simulatorBacked(spec schemas.ServiceSpec) bool {
	return spec.Role == schemas.ServiceRoleUser || (spec.Gateway != nil && spec.Gateway.Provider == "" && spec.Gateway.Model == "")
}

// UserOnlyServices are the ids of the user services a remote agent is not
// sent when the bench drives the conversation. A separate customer service
// (helpdesk plus customer) carries only respond_to_user, so it is dropped
// when the run has another visible service for the agent's tools. A tau2
// domain server that holds its own user is the run's only service and also
// carries the task's tools, so it stays; the launchers that list tools
// leave respond_to_user out themselves. It is pure.
func UserOnlyServices(services []schemas.ServiceHandle) map[string]bool {
	users, others := map[string]bool{}, 0
	for _, h := range services {
		if h.Spec.Hidden {
			continue
		}
		if simulatorBacked(h.Spec) {
			users[h.Spec.ID] = true
		} else {
			others++
		}
	}
	if others == 0 {
		return nil
	}
	return users
}

// DropServices returns urls without the ids in drop. It is pure.
func DropServices[V any](urls map[string]V, drop map[string]bool) map[string]V {
	if len(drop) == 0 || urls == nil {
		return urls
	}
	out := make(map[string]V, len(urls))
	for id, v := range urls {
		if !drop[id] {
			out[id] = v
		}
	}
	return out
}

// ConversationEnv adds the bench-driven conversation's launcher env when c
// asks the bench to drive and the run has a simulated user, and reports
// whether it did. BENCH_CONV_USERS holds the user servers (BENCH_MCP_SERVERS
// shape), BENCH_CONV_MAX_TURNS, BENCH_CONV_HISTORY and BENCH_CONV_GREETING
// the settings. A run with no simulated user gets nothing: the agent runs
// once, as without a conversation block.
func ConversationEnv(rc *schemas.RunContext, c *schemas.ConversationSpec, env map[string]string) (bool, error) {
	if !c.BenchDriven() || rc == nil {
		return false, nil
	}
	key := ""
	if rc.Key != nil {
		key = rc.Key.Value
	}
	urls, js, err := UserServers(rc.Services, key)
	if err != nil {
		return false, err
	}
	if len(urls) == 0 {
		return false, nil
	}
	turns := c.MaxTurns
	if turns <= 0 {
		turns = simulatorTurns(rc)
	}
	history := c.History
	if history == "" {
		history = schemas.ConversationHistoryClient
	}
	greeting := strings.TrimSpace(c.Greeting)
	if greeting == "" {
		greeting = schemas.DefaultConversationGreeting
	}
	env["BENCH_CONV_USERS"] = js
	env["BENCH_CONV_MAX_TURNS"] = fmt.Sprint(turns)
	env["BENCH_CONV_HISTORY"] = history
	env["BENCH_CONV_GREETING"] = greeting
	return true, nil
}

// simulatorTurns is the effective simulator's max_turns, else 12.
func simulatorTurns(rc *schemas.RunContext) int {
	if rc.Experiment != nil && rc.Experiment.Simulator != nil && rc.Experiment.Simulator.MaxTurns > 0 {
		return min(rc.Experiment.Simulator.MaxTurns, schemas.MaxConversationTurns)
	}
	if rc.Suite != nil && rc.Suite.Simulator != nil && rc.Suite.Simulator.MaxTurns > 0 {
		return min(rc.Suite.Simulator.MaxTurns, schemas.MaxConversationTurns)
	}
	return 12
}

// IsPullAgent reports whether cfg is a pull-mode agent.
func IsPullAgent(cfg *schemas.AgentConfig) bool {
	return cfg != nil && (cfg.Adapter == AdapterPull || cfg.Pull != nil)
}

// PullAgentName is the name a pull agent's process serves under: pull.name,
// else the agent id.
func PullAgentName(cfg *schemas.AgentConfig) string {
	if cfg != nil && cfg.Pull != nil && cfg.Pull.Name != "" {
		return cfg.Pull.Name
	}
	if cfg != nil {
		return cfg.ID
	}
	return ""
}

// CheckPullName refuses a name heimdall.serve cannot register. It is pure.
func CheckPullName(name string) error {
	if !pullName.MatchString(name) {
		return fmt.Errorf("pull agent name %q: want letters, digits, '_', '.' or '-', at most 128, starting with a letter or digit", name)
	}
	return nil
}

// CheckPullAgent refuses a pull block the adapter cannot run. It is pure.
func CheckPullAgent(cfg *schemas.AgentConfig) error {
	if cfg == nil || cfg.Pull == nil {
		return fmt.Errorf("adapter pull needs a pull block (an empty one serves under the agent id)")
	}
	p := cfg.Pull
	if err := CheckPullName(PullAgentName(cfg)); err != nil {
		return err
	}
	switch p.Tools {
	case "", schemas.OpenAIToolsAuto, schemas.OpenAIToolsOff:
	default:
		return fmt.Errorf("pull.tools %q: want auto or off", p.Tools)
	}
	if err := CheckRepoMode(p.Repo, "pull"); err != nil {
		return err
	}
	if p.TimeoutS < 0 || p.TurnTimeoutS < 0 || p.MaxToolRounds < 0 {
		return fmt.Errorf("pull.timeout_s, pull.turn_timeout_s and pull.max_tool_rounds cannot be negative")
	}
	return CheckConversation(p.Conversation, "pull")
}

// CheckRepoMode refuses an unknown repo value. It is pure.
func CheckRepoMode(mode, block string) error {
	switch mode {
	case "", schemas.RepoNone, schemas.RepoTarball:
		return nil
	}
	return fmt.Errorf("%s.repo %q: want tarball or none", block, mode)
}

// PullOutputAllowed says whether a pull agent can produce a task's output
// kind, and why not. Answer always; state with tools on (its tool calls
// run through the run's tool proxy); diff with repo tarball (its patch is
// applied to the workspace); artifact never. It is pure.
func PullOutputAllowed(p *schemas.PullAgent, kind string) (bool, string) {
	switch kind {
	case schemas.OutputAnswer:
		return true, ""
	case schemas.OutputState:
		if p == nil || p.Tools != schemas.OpenAIToolsOff {
			return true, ""
		}
		return false, "a state task needs pull.tools auto: a pull agent changes the run's services only through the tool calls it returns"
	case schemas.OutputDiff:
		if p != nil && p.Repo == schemas.RepoTarball {
			return true, ""
		}
		return false, "a coding task needs pull.repo tarball: the agent gets the repository as a tarball and returns a patch"
	}
	return false, "a pull agent runs outside the workspace, so it cannot produce a " + kind + " output"
}

// MCPServersJSONExcept is MCPServersJSONFor without the ids in drop.
func MCPServersJSONExcept(services []schemas.ServiceHandle, key string, drop map[string]bool) (string, error) {
	var keep []schemas.ServiceHandle
	for _, h := range services {
		if !drop[h.Spec.ID] {
			keep = append(keep, h)
		}
	}
	return MCPServersJSONFor(keep, key)
}
