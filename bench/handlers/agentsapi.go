package handlers

// Agents as code (lane Y10, gap G1 P1-5): POST /agents creates an agent
// from its YAML or from a template and its params, and POST
// /agents/{id}/test sends a saved agent one sample task. API v1 serves
// them with GET and PUT /agents/{id} (the config routes), so CI can
// create, update, version and test agents without the web UI.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// AgentCreateRequest is POST /agents: either yaml (the whole agent file)
// or template with its params (the fields of POST /agents/from-template).
type AgentCreateRequest struct {
	YAML string `json:"yaml,omitempty"`
	AgentFromTemplateRequest
}

// reservedAgentIDs are names the web UI uses as paths under /agents.
var reservedAgentIDs = map[string]bool{"new": true, "diff": true, "templates": true, "config": true, "test-endpoint": true,
	"from-template": true, "endpoint-presets": true, "endpoint-preview": true}

type agentsAPI struct {
	b *agentBuilder
	t *agentTestHandler
}

func init() {
	RegisterOrgArea("agentsapi", func(env OrgEnv, r *router.Router) {
		h := &agentsAPI{b: &agentBuilder{mode: env.Mode, projects: env.Services.Projects},
			t: &agentTestHandler{env: env, client: endpointTestClient, now: time.Now}}
		r.POST(APIPrefix+"/agents", env.W(h.create))
		r.POST(APIPrefix+"/agents/{id}/test", env.W(h.test))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents", Tag: "agents",
			Summary: "Create an agent from its YAML (yaml) or from a template and its params (template, id, params, model); 409 when the name is taken. dry_run validates and writes nothing",
			Request: AgentCreateRequest{}, Response: AgentFromTemplateResponse{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/{id}/test", Tag: "agents",
			Summary:  "Send a saved HTTP, OpenAI-compatible or A2A agent one sample task and return its answer, the requests sent and a trace (ok false says why)",
			Response: EndpointTestResult{}},
	)
}

func (h *agentsAPI) create(ctx *fasthttp.RequestCtx) {
	var req AgentCreateRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.b.badRequest(ctx, err.Error())
		return
	}
	if strings.TrimSpace(req.YAML) == "" {
		if req.Template == "" {
			h.b.badRequest(ctx, "send yaml (the agent file) or template with its params: GET /agents/templates lists the templates")
			return
		}
		h.b.createFromTemplate(ctx, req.AgentFromTemplateRequest)
		return
	}
	if req.Template != "" || len(req.Params) > 0 || req.Model != nil || req.Notes != "" {
		h.b.badRequest(ctx, "send yaml or template, not both")
		return
	}
	if len(req.YAML) > projectfs.MaxFileBytes {
		lib.SendError(ctx, h.b.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, "YAML is over 1 MiB")
		return
	}
	doc := []byte(req.YAML)
	cfg, err := decodeAgent(doc, "agents/agent.yaml")
	if err != nil {
		h.b.badRequest(ctx, err.Error())
		return
	}
	id := cfg.ID
	if req.ID = strings.TrimSpace(req.ID); req.ID != "" && req.ID != id {
		h.b.badRequest(ctx, fmt.Sprintf("id %q does not match the id in the YAML (%s)", req.ID, id))
		return
	}
	if !agentIDRe.MatchString(id) {
		h.b.badRequest(ctx, fmt.Sprintf("agent name %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit (max 63)", id))
		return
	}
	if reservedAgentIDs[id] {
		h.b.badRequest(ctx, fmt.Sprintf("agent name %q is reserved by the web UI: pick another", id))
		return
	}
	rel := "agents/" + id + ".yaml"
	out := AgentFromTemplateResponse{ID: id, Path: rel, Files: []string{rel}, YAML: req.YAML, DryRun: req.DryRun}
	if req.DryRun {
		lib.SendJSON(ctx, h.b.mode, fasthttp.StatusOK, out)
		return
	}
	if h.b.noStore(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	for _, rp := range []string{id + ".yaml", path.Join("images", id)} {
		_, _, err := h.b.projects.Read(work, projectfs.AreaAgents, rp)
		if err == nil || errors.Is(err, projectfs.ErrInvalid) { // ErrInvalid: it is a folder
			lib.SendError(ctx, h.b.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				fmt.Sprintf("an agent named %s already exists (agents/%s): update it with PUT /agents/%s or pick another name", id, rp, id))
			return
		}
		if !errors.Is(err, projectfs.ErrNotFound) {
			h.b.storeError(ctx, "read agents/"+rp, err)
			return
		}
	}
	opts := projectfs.WriteOpts{Actor: abActor(ctx), Note: "agents API: create"}
	if _, err := h.b.projects.Write(work, projectfs.AreaAgents, id+".yaml", doc, opts); err != nil {
		h.b.storeError(ctx, "write "+rel, err)
		return
	}
	snapshotAgentSave(work, h.b.projects, id+".yaml", id, doc, opts.Actor, "created through the API")
	lib.SendJSON(ctx, h.b.mode, fasthttp.StatusCreated, out)
}

func (h *agentsAPI) test(ctx *fasthttp.RequestCtx) {
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.t.bad(ctx, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return
	}
	if len(ctx.PostBody()) > 0 && strings.TrimSpace(string(ctx.PostBody())) != "{}" {
		h.t.bad(ctx, "this route takes no body: it tests the saved agent")
		return
	}
	cfg, err := (&agentCompatHandler{env: h.t.env}).agentConfig(ctx, id)
	if err != nil {
		lib.SendError(ctx, h.t.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
		return
	}
	work, cancel := context.WithTimeout(context.Background(), EndpointTestMaxWait+5*time.Second)
	defer cancel()
	lib.SendJSON(ctx, h.t.env.Mode, fasthttp.StatusOK, h.t.run(work, cfg))
}
