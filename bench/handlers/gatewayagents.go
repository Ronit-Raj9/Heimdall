package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/modelswitch"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// W4 feature 11: benchmark an agent registered in Bifrost's agent gateway
// (audit/W4_DESIGN_3_BIFROST.md section 14). The list reads the org's own
// Bifrost; the import only builds the agent YAML, which the page then posts
// to the normal POST /agents. Nothing is written here.

func init() {
	RegisterOrgArea("gatewayagents", func(env OrgEnv, r *router.Router) {
		newGatewayAgentsHandler(env).routes(r)
	})
}

type gatewayAgentsHandler struct {
	env       OrgEnv
	sourceGet func(ctx context.Context, env OrgEnv) func(ctx context.Context, path string, out any) error
}

func newGatewayAgentsHandler(env OrgEnv) *gatewayAgentsHandler {
	return &gatewayAgentsHandler{env: env, sourceGet: gatewaySourceGet}
}

func (h *gatewayAgentsHandler) routes(r *router.Router) {
	r.GET(APIPrefix+"/gateway/agents", h.env.R(h.list))
	r.POST(APIPrefix+"/gateway/agents/{name}/import", h.env.W(h.importAgent))
}

// GatewayAgent is one agent registration of GET /gateway/agents.
type GatewayAgent struct {
	Name    string `json:"name"`
	CardURL string `json:"card_url"`
	Enabled bool   `json:"enabled"`
}

// gatewayAgentImportRequest is the body of POST /gateway/agents/{name}/import.
type gatewayAgentImportRequest struct {
	Model    string `json:"model"`
	Provider string `json:"provider"`
	ID       string `json:"id,omitempty"`
}

// registrations reads GET /api/agents of the org's Bifrost, answering the
// error itself when it cannot.
func (h *gatewayAgentsHandler) registrations(ctx *fasthttp.RequestCtx) ([]schemas.A2ARegistrationInfo, bool) {
	get := h.sourceGet(ctx, h.env)
	if get == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Connect your Bifrost first")
		return nil, false
	}
	var page struct {
		Agents []schemas.A2ARegistrationInfo `json:"agents"`
	}
	if err := get(ctx, "/api/agents", &page); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, "Heimdall could not reach your Bifrost: "+plainGatewayError(err))
		return nil, false
	}
	return page.Agents, true
}

func (h *gatewayAgentsHandler) list(ctx *fasthttp.RequestCtx) {
	regs, ok := h.registrations(ctx)
	if !ok {
		return
	}
	out := make([]GatewayAgent, 0, len(regs))
	for _, r := range regs {
		out = append(out, GatewayAgent{Name: r.Name, CardURL: r.CardURL, Enabled: r.Enabled})
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"agents": out})
}

// importedAgentFold is what an agent id may not contain.
var importedAgentFold = regexp.MustCompile(`[^a-z0-9._-]+`)

// importedAgentID is the default agent id of a registration: its name
// folded to the agent id pattern, prefixed bifrost-.
func importedAgentID(name string) string {
	s := strings.Trim(importedAgentFold.ReplaceAllString(strings.ToLower(name), "-"), "-._")
	id := "bifrost-" + s
	if s == "" {
		id = "bifrost-agent"
	}
	if len(id) > 63 {
		id = strings.TrimRight(id[:63], "-._")
	}
	return id
}

// importedAgentYAML is the a2a agent config of one registration, in the
// field order of the reference a2a agents.
type importedAgentYAML struct {
	ID      string `yaml:"id"`
	Adapter string `yaml:"adapter"`
	Image   string `yaml:"image"`
	Model   string `yaml:"model"`
	A2A     struct {
		CardURL  string `yaml:"card_url"`
		TimeoutS int    `yaml:"timeout_s"`
	} `yaml:"a2a"`
	Budgets struct {
		WallS int `yaml:"wall_s"`
		CPUs  int `yaml:"cpus"`
		MemGB int `yaml:"mem_gb"`
	} `yaml:"budgets"`
	Gateway struct {
		Provider   string `yaml:"provider"`
		MaxRetries int    `yaml:"max_retries"`
	} `yaml:"gateway"`
}

func (h *gatewayAgentsHandler) importAgent(ctx *fasthttp.RequestCtx) {
	name, _ := ctx.UserValue("name").(string)
	if u, err := url.PathUnescape(name); err == nil {
		name = u
	}
	var req gatewayAgentImportRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the import request could not be read: "+err.Error())
		return
	}
	req.Model, req.Provider, req.ID = strings.TrimSpace(req.Model), strings.TrimSpace(req.Provider), strings.TrimSpace(req.ID)
	if req.Model == "" || req.Provider == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Pick the model and the provider the agent uses.")
		return
	}
	if req.ID != "" && !agentIDRe.MatchString(req.ID) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "An agent id uses lower case letters, digits, dots, hyphens and underscores, and starts with a letter or digit.")
		return
	}
	regs, ok := h.registrations(ctx)
	if !ok {
		return
	}
	var reg *schemas.A2ARegistrationInfo
	for i := range regs {
		if regs[i].Name == name {
			reg = &regs[i]
			break
		}
	}
	switch {
	case reg == nil:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("Your Bifrost has no agent registration named %s.", name))
		return
	case !reg.Enabled:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "This agent registration is switched off in your Bifrost. Switch it on there first.")
		return
	case strings.TrimSpace(reg.CardURL) == "":
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "This agent registration has no agent card address.")
		return
	}
	if h.env.Services.Hosted {
		if _, err := modelswitch.CheckGatewayURL(reg.CardURL, true); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Heimdall's gateway must reach this agent's card at a public https address")
			return
		}
	}
	var y importedAgentYAML
	y.ID = req.ID
	if y.ID == "" {
		y.ID = importedAgentID(reg.Name)
	}
	y.Adapter, y.Image, y.Model = "a2a", "bench/coding-core-base:1", req.Model
	y.A2A.CardURL, y.A2A.TimeoutS = reg.CardURL, 600
	y.Budgets.WallS, y.Budgets.CPUs, y.Budgets.MemGB = 900, 1, 1
	y.Gateway.Provider, y.Gateway.MaxRetries = req.Provider, 0
	body, err := yaml.Marshal(y)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the agent file could not be written")
		return
	}
	comment := "# Imported from Bifrost agent registration " + strings.NewReplacer("\n", " ", "\r", " ").Replace(reg.Name) + "\n"
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"agent_id": y.ID, "yaml": comment + string(body)})
}
