package handlers

import (
	"context"
	"regexp"
	"sort"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Evidence levels an agent config's runs get (AgentSummary.EvidenceLevel).
const (
	agentEvidenceNative  = "native"  // a parser reads the agent's own stream
	agentEvidenceGateway = "gateway" // no parser: Bifrost's rows only
)

// AgentConfigFile is one agents/*.yaml file as the agent source read it.
// Config is nil when the file failed to decode, and Err says why.
type AgentConfigFile struct {
	Path   string // relative to the bench root, slash-separated
	Config *schemas.AgentConfig
	Err    error
}

// AgentSource lists the agent configs on disk. cmd/bifrost-bench satisfies
// it with the same schema-checked decoder the planner uses.
type AgentSource interface {
	AgentConfigs(ctx context.Context) ([]AgentConfigFile, error)
}

// AgentsHandler serves the agent configs a project holds. It never serves an
// environment value or the content of a delivered file: values can hold keys
// and templates can hold anything, so only names and paths leave the server.
type AgentsHandler struct {
	mode   schemas.Mode
	agents AgentSource
	index  ReadIndex
	// hideControls drops the scripted control agents (oracle, noop, toy,
	// ...) from the list. They are not a user's agent; the admission checks
	// still use them by path.
	hideControls bool
}

// NewAgentsHandler creates the agents handler. agents and index may be nil;
// without an index every sealed run count is null.
func NewAgentsHandler(mode schemas.Mode, agents AgentSource, index ReadIndex) *AgentsHandler {
	return &AgentsHandler{mode: mode, agents: agents, index: index}
}

// WithHiddenControls hides the scripted control agents from GET /agents
// when hide is set (every production server; local development and tests
// keep them, benchcatalog.DevContent).
func (h *AgentsHandler) WithHiddenControls(hide bool) *AgentsHandler {
	h.hideControls = hide
	return h
}

// IsControlAgent reports whether c is a scripted control agent (adapter
// scripted: oracle, noop, toy, refuse, ...), never a user's own agent.
func IsControlAgent(c *schemas.AgentConfig) bool {
	return c != nil && (c.Adapter == "scripted" || c.Scripted != nil)
}

// RegisterRoutes registers GET /agents.
func (h *AgentsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/agents", lib.ChainMiddlewares(h.getAgents, middlewares...))
}

// getAgents handles GET /api/bench/agents. A config that fails to decode is
// listed with its error, never dropped.
func (h *AgentsHandler) getAgents(ctx *fasthttp.RequestCtx) {
	if h.agents == nil {
		sendUnavailable(ctx, h.mode, "agent configs")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	files, err := h.agents.AgentConfigs(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list agent configs", err)
		return
	}
	counts := h.sealedRuns()
	out := schemas.AgentList{Agents: make([]schemas.AgentSummary, 0, len(files))}
	for _, f := range files {
		if h.hideControls && IsControlAgent(f.Config) {
			continue
		}
		out.Agents = append(out.Agents, summarizeAgent(f, counts))
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// sealedRuns counts sealed runs per agent config id over every experiment,
// or returns nil without an index.
func (h *AgentsHandler) sealedRuns() map[string]int {
	if h.index == nil {
		return nil
	}
	counts := map[string]int{}
	for _, exp := range h.index.Experiments() {
		for _, rv := range h.index.Runs(exp) {
			if rv.Sealed && rv.Run.Agent.ConfigID != "" {
				counts[rv.Run.Agent.ConfigID]++
			}
		}
	}
	return counts
}

// summarizeAgent builds one config's entry: names, never values.
func summarizeAgent(f AgentConfigFile, counts map[string]int) schemas.AgentSummary {
	out := schemas.AgentSummary{Path: f.Path, EnvNames: []string{}, FilePaths: []string{}}
	if f.Err != nil || f.Config == nil {
		out.Error = "could not read the agent config"
		if f.Err != nil {
			out.Error = redactQuoted(f.Err.Error())
		}
		return out
	}
	c := f.Config
	out.ID, out.Adapter, out.Image, out.Model, out.Parser = c.ID, c.Adapter, c.Image, c.Model, c.Parser
	out.Capabilities = schemas.EffectiveCapabilities(c)
	out.NotesProjectFile, out.NotesUserFile = c.Notes.ProjectFile, c.Notes.UserFile
	out.Budgets = c.Budgets
	out.Gateway = c.Gateway
	if out.Gateway.Fallbacks != nil {
		out.Gateway.Fallbacks = append([]string(nil), c.Gateway.Fallbacks...)
	}
	if c.Build != nil {
		out.Dockerfile, out.BaseArg = c.Build.Dockerfile, c.Build.BaseArg
		out.PerSuiteBuild = c.Build.BaseArg != ""
	}
	if c.Scripted != nil {
		out.ScriptedKind = c.Scripted.Kind
	}
	out.EvidenceLevel = agentEvidenceGateway
	if c.Parser != "" {
		out.EvidenceLevel = agentEvidenceNative
	}
	if c.Parser == "generic_jsonl" { // Agent events area: native agent events on stdout
		out.ReportsEvents = "native"
	}
	for name := range c.Env {
		out.EnvNames = append(out.EnvNames, name)
	}
	sort.Strings(out.EnvNames)
	for path := range c.Files {
		out.FilePaths = append(out.FilePaths, path)
	}
	sort.Strings(out.FilePaths)
	if counts != nil {
		n := counts[c.ID]
		out.SealedRuns = &n
	}
	return out
}

// quotedValue matches a value a decoder error quotes (`...`, '...' or "...").
var quotedValue = regexp.MustCompile("`[^`]*`|'[^']*'|\"[^\"]*\"")

// redactQuoted drops the values a decode error quotes. A YAML or schema error
// can echo the offending value, and an agent config's values can be keys;
// the file and field names it reports are enough to fix the file.
func redactQuoted(msg string) string {
	return quotedValue.ReplaceAllString(msg, "[value hidden]")
}
