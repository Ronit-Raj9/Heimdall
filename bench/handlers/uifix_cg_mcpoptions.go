package handlers

import (
	"net/url"
	"sort"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// MCP test options (fix wave M7-95): GET /gateway/mcp-options?suite=<id>
// lists, for one benchmark the caller can read, what the MCP tools feature
// test dialog picks from: the tool services (tools are named
// service/tool; the tool names themselves are only known when a run lists
// them) and the probes that poison tool descriptions (the poison_probe
// option). Names and ids only. A benchmark GET /suites/{id} does not show
// the caller is a 404 here too.
const MCPOptionsPath = APIPrefix + "/gateway/mcp-options"

// MCPOptions is GET /gateway/mcp-options.
type MCPOptions struct {
	Suite        string           `json:"suite"`
	Services     []string         `json:"services"`
	PoisonProbes []MCPPoisonProbe `json:"poison_probes"`
}

// MCPPoisonProbe is one probe that poisons tool descriptions.
type MCPPoisonProbe struct {
	ID   string `json:"id"`
	Mode string `json:"mode"` // describe_poison | rug_pull | shadow | schema_poison
}

var mcpPoisonModes = map[string]bool{"describe_poison": true, "rug_pull": true, "shadow": true, "schema_poison": true}

type mcpOptionsHandler struct {
	env OrgEnv
	r   *router.Router
}

func init() {
	RegisterOrgArea("uifix_cg_mcpoptions", func(env OrgEnv, r *router.Router) {
		h := &mcpOptionsHandler{env: env, r: r}
		r.GET(MCPOptionsPath, env.R(h.get))
	})
	APIRoutes = append(APIRoutes, RouteDoc{Method: "GET", Path: MCPOptionsPath, Tag: "gateway",
		Summary:  "For one benchmark: its tool and MCP services and its probes that poison tool descriptions (ids and modes), the pickers of the MCP tools feature test",
		Query:    []QueryDoc{{Name: "suite", Description: "The benchmark id (required)"}},
		Response: MCPOptions{}})
}

func (h *mcpOptionsHandler) get(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	id := strings.TrimSpace(string(ctx.QueryArgs().Peek("suite")))
	if id == "" || len(id) > 200 {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "suite is required (a benchmark id)")
		return
	}
	src := h.env.Services.Suites
	if src == nil {
		sendUnavailable(ctx, mode, "suite loader")
		return
	}
	var seen map[string]any
	if !subGet(h.r, ctx, APIPrefix+"/suites/"+url.PathEscape(id), &seen) {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no benchmark "+id)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := src.LoadSuite(work, id)
	if err != nil || s == nil {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no benchmark "+id)
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, mcpOptionsOf(s))
}

// mcpOptionsOf reads the options from a loaded suite.
func mcpOptionsOf(s *schemas.Suite) MCPOptions {
	out := MCPOptions{Suite: s.ID, Services: []string{}, PoisonProbes: []MCPPoisonProbe{}}
	svc := map[string]bool{}
	add := func(list []schemas.ServiceSpec) {
		for _, sv := range list {
			if !sv.Hidden && (sv.Role == "tool" || sv.Role == "mcp") && sv.ID != "" {
				svc[sv.ID] = true
			}
		}
	}
	add(s.Services)
	for _, t := range s.Tasks {
		if t != nil {
			add(t.Services)
		}
	}
	for n := range svc {
		out.Services = append(out.Services, n)
	}
	sort.Strings(out.Services)
	for _, p := range s.Probes {
		if p != nil && p.Apply.Tool != nil && mcpPoisonModes[p.Apply.Tool.Mode] {
			out.PoisonProbes = append(out.PoisonProbes, MCPPoisonProbe{ID: p.ID, Mode: p.Apply.Tool.Mode})
		}
	}
	return out
}
