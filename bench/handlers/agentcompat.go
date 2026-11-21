package handlers

// Agent compatibility (agent-first contract, section 1): which benchmarks an
// agent can run. GET /agents/{id}/compatibility checks the agent's
// capabilities (its capabilities: list or its adapter's defaults) against
// every benchmark the org sees, built-in library benchmarks included, with
// compat.Check, the same rule the planner refuses arms with.

import (
	"fmt"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// BenchmarkCompatibility is one benchmark's row of AgentCompatibility.
type BenchmarkCompatibility struct {
	ID       string   `json:"id"`
	OK       bool     `json:"ok"`
	Requires []string `json:"requires"`
	Missing  []string `json:"missing"`
	Reason   string   `json:"reason"`
}

// AgentCompatibility is GET /agents/{id}/compatibility.
type AgentCompatibility struct {
	Agent        string                   `json:"agent"`
	Capabilities []string                 `json:"capabilities"`
	Defaulted    bool                     `json:"defaulted"` // the agent lists no capabilities: of its own
	Benchmarks   []BenchmarkCompatibility `json:"benchmarks"`
}

type agentCompatHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("agentcompat", registerAgentCompat)
}

// registerAgentCompat registers GET /agents/{id}/compatibility.
func registerAgentCompat(env OrgEnv, r *router.Router) {
	h := &agentCompatHandler{env: env}
	r.GET(APIPrefix+"/agents/{id}/compatibility", env.R(h.get))
}

func (h *agentCompatHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

func (h *agentCompatHandler) get(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return
	}
	src := h.env.Services.Suites
	if src == nil {
		sendUnavailable(ctx, h.env.Mode, "benchmarks")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	cfg, err := h.agentConfig(ctx, id)
	if err != nil {
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	ids, err := src.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "list benchmarks", err)
		return
	}
	out := AgentCompatibility{
		Agent:        cfg.ID,
		Capabilities: schemas.EffectiveCapabilities(cfg).Strings(),
		Defaulted:    len(cfg.Capabilities) == 0,
		Benchmarks:   make([]BenchmarkCompatibility, 0, len(ids)),
	}
	for _, sid := range ids {
		row := BenchmarkCompatibility{ID: sid, Requires: []string{}, Missing: []string{}}
		s, err := src.LoadSuite(work, sid)
		if err != nil || s == nil {
			row.Reason = "could not load this benchmark"
			out.Benchmarks = append(out.Benchmarks, row)
			continue
		}
		row.Requires = suite.Requires(s).Strings()
		ok, missing, reason := compat.Check(cfg, s)
		row.OK, row.Reason = ok, reason
		row.Missing = schemas.CapabilitySet(missing).Strings()
		out.Benchmarks = append(out.Benchmarks, row)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &out)
}

// agentConfig finds agent id: through the org's agent source (which holds
// built-in library agents too) when the server has one, else the project's
// agents/<id>.yaml.
func (h *agentCompatHandler) agentConfig(ctx *fasthttp.RequestCtx, id string) (*schemas.AgentConfig, error) {
	if src := h.env.Services.Agents; src != nil {
		if files, err := src.AgentConfigs(ctx); err == nil {
			for _, f := range files {
				if f.Config != nil && f.Config.ID == id {
					return f.Config, nil
				}
			}
		}
	}
	if ps := h.env.Services.Projects; ps != nil {
		for _, ext := range []string{".yaml", ".yml"} {
			data, _, err := ps.Read(ctx, projectfs.AreaAgents, id+ext)
			if err != nil {
				continue
			}
			var cfg schemas.AgentConfig
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("agent %q: could not read its config: %v", id, err)
			}
			if cfg.ID == "" {
				cfg.ID = id
			}
			return &cfg, nil
		}
	}
	return nil, fmt.Errorf("agent %q not found: create it first (Agents, New agent) or check agents/%s.yaml", id, id)
}

func init() {
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/{id}/compatibility", Tag: "agents",
			Summary: "Which benchmarks an agent can run: its capabilities against what each benchmark needs, with the missing capabilities and the reason", Response: AgentCompatibility{}},
	)
}
