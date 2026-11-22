package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/platformimport"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// Import an agent by its platform id (Wave B lane Z16, INFRA_I2 item 9):
// the org names a platform (Vapi, Retell, ElevenLabs Agents, Bedrock
// AgentCore, Agentforce, Copilot Studio, Vertex AI Agent Engine, LangGraph
// Platform), the agent's id there and the org secrets that unlock the
// platform API. Heimdall reads the agent definition through that API and
// writes agents/<id>.yaml with the right endpoint preset, plus
// agents/imported/<id>.json with what it read (prompt, first message,
// tools, voice settings).

// AgentPlatformList is GET /agents/platforms.
type AgentPlatformList struct {
	Platforms []platformimport.Platform `json:"platforms"`
}

// AgentImportRequest is POST /agents/import-platform.
type AgentImportRequest struct {
	// ID is the new Heimdall agent's name.
	ID       string            `json:"id"`
	Platform string            `json:"platform"`
	AgentID  string            `json:"agent_id"`
	Inputs   map[string]string `json:"inputs,omitempty"`
	// Secrets maps each secret role of the platform (GET /agents/platforms)
	// to the name of an org secret (Settings > Secrets).
	Secrets map[string]string `json:"secrets,omitempty"`
	// DryRun reads the platform and returns the config without saving it.
	DryRun bool `json:"dry_run,omitempty"`
}

// AgentImportResponse is the imported agent.
type AgentImportResponse struct {
	ID     string                 `json:"id"`
	Path   string                 `json:"path"`
	Files  []string               `json:"files"`
	YAML   string                 `json:"yaml"`
	DryRun bool                   `json:"dry_run"`
	Import *platformimport.Result `json:"import"`
	// ValidationError is set on a dry run whose config this server's agent
	// schema refuses (a save would fail with it).
	ValidationError string `json:"validation_error,omitempty"`
}

// agentImportWait bounds one import (every platform call together).
const agentImportWait = 45 * time.Second

type agentImportHandler struct {
	env OrgEnv
	// client returns the HTTP client of platform calls (guarded on a
	// hosted server, no redirects).
	client  func(hosted bool, timeout time.Duration) *http.Client
	rewrite func(*url.URL) // tests only
	now     func() time.Time
	// validate checks the rendered YAML (the agent schema decoder).
	validate func(doc []byte, rel string) error
}

func newAgentImportHandler(env OrgEnv) *agentImportHandler {
	return &agentImportHandler{env: env, client: endpointTestClient, now: time.Now,
		validate: func(doc []byte, rel string) error { _, err := decodeAgent(doc, rel); return err }}
}

func init() {
	RegisterOrgArea("agentimport", func(env OrgEnv, r *router.Router) {
		h := newAgentImportHandler(env)
		r.GET(APIPrefix+"/agents/platforms", env.R(h.platforms))
		r.POST(APIPrefix+"/agents/import-platform", env.W(h.importAgent))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/agents/platforms", Tag: "agents",
			Summary:  "Agent platforms an agent can be imported from by id (Vapi, Retell, ElevenLabs, Bedrock AgentCore, Agentforce, Copilot Studio, Vertex AI, LangGraph Platform), with the inputs and secrets each needs",
			Response: AgentPlatformList{}},
		RouteDoc{Method: "POST", Path: APIPrefix + "/agents/import-platform", Tag: "agents",
			Summary: "Read an agent's definition from its platform by id with org secrets, and create agents/<id>.yaml wired to the platform's endpoint preset (dry_run returns it without saving)",
			Auth:    AuthWrite, Request: AgentImportRequest{}, Status: fasthttp.StatusCreated, Response: AgentImportResponse{}},
	)
}

func (h *agentImportHandler) platforms(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, AgentPlatformList{Platforms: platformimport.Platforms})
}

func (h *agentImportHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

// agentImportReserved are names the web UI and these routes use.
var agentImportReserved = map[string]bool{"new": true, "diff": true, "templates": true, "config": true, "platforms": true, "import-platform": true}

func (h *agentImportHandler) importAgent(ctx *fasthttp.RequestCtx) {
	var req AgentImportRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if !agentIDRe.MatchString(req.ID) {
		h.bad(ctx, fmt.Sprintf("agent name %q: use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit (max 63)", req.ID))
		return
	}
	if agentImportReserved[req.ID] {
		h.bad(ctx, fmt.Sprintf("agent name %q is reserved by the web UI: pick another", req.ID))
		return
	}
	preq := platformimport.Request{Platform: strings.TrimSpace(req.Platform), AgentID: req.AgentID, Inputs: req.Inputs, Secrets: req.Secrets}
	if _, err := platformimport.Check(preq); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	store := h.env.Services.Projects
	if !req.DryRun && store == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: start it with a project folder (serve -root) to create agents")
		return
	}
	rel := "agents/" + req.ID + ".yaml"
	side := platformimport.SidecarPath(req.ID)
	work, cancel := context.WithTimeout(context.Background(), agentImportWait)
	defer cancel()
	if !req.DryRun {
		for _, rp := range []string{req.ID + ".yaml", side} {
			if _, _, err := store.Read(work, projectfs.AreaAgents, rp); err == nil {
				lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
					fmt.Sprintf("an agent named %s already exists (agents/%s): pick another name", req.ID, rp))
				return
			}
		}
	}
	secrets, err := (&agentTestHandler{env: h.env}).secrets(work, platformimport.SecretNames(preq)...)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	hosted := h.env.Services.Hosted
	c := &platformimport.Client{HTTP: h.client(hosted, 20*time.Second), Now: h.now, Rewrite: h.rewrite,
		CheckURL: func(raw string) error { return (&agentTestHandler{env: h.env}).checkURL(raw) }}
	res, err := c.Import(work, preq, secrets)
	if err != nil {
		// The platform is down or unreachable: 502. It refused the key,
		// has no such agent or the input is wrong: 422.
		status := fasthttp.StatusUnprocessableEntity
		var se *platformimport.StatusError
		if errors.As(err, &se) && se.Status >= 500 || strings.Contains(err.Error(), "did not answer") {
			status = fasthttp.StatusBadGateway
		}
		lib.SendError(ctx, h.env.Mode, status, lib.ErrTypeBadRequest, "import from "+preq.Platform+": "+err.Error())
		return
	}
	def := platformimport.AgentDefaults{}
	if t, ok := findTemplate("http-url"); ok {
		def.Model = t.DefaultModel
		if len(t.Providers) > 0 {
			def.Provider = t.Providers[0]
		}
	}
	doc, err := platformimport.AgentYAML(req.ID, res, def)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	sideDoc, err := platformimport.SidecarJSON(res)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	out := AgentImportResponse{ID: req.ID, Path: rel, Files: []string{rel, "agents/" + side}, YAML: string(doc), DryRun: req.DryRun, Import: res}
	if err := h.validate(doc, rel); err != nil {
		if req.DryRun {
			out.ValidationError = err.Error()
			lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
			return
		}
		h.bad(ctx, "the imported agent does not validate on this server: "+err.Error())
		return
	}
	if req.DryRun {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	opts := projectfs.WriteOpts{Actor: abActor(ctx), Note: "agent import: " + preq.Platform + " " + res.AgentID}
	// The definition first, the YAML last: a half-written agent never shows up in the list.
	if _, err := store.Write(work, projectfs.AreaAgents, side, sideDoc, opts); err != nil {
		(&agentBuilder{mode: h.env.Mode}).storeError(ctx, "write agents/"+side, err)
		return
	}
	if _, err := store.Write(work, projectfs.AreaAgents, path.Clean(req.ID+".yaml"), doc, opts); err != nil {
		(&agentBuilder{mode: h.env.Mode}).storeError(ctx, "write "+rel, err)
		return
	}
	snapshotAgentSave(work, store, req.ID+".yaml", req.ID, doc, opts.Actor, "imported from "+preq.Platform)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}
