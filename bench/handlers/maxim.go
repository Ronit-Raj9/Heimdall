package handlers

// Maxim integration (X23, the hackathon sponsor): an org connects its Maxim
// log repository once (API key in the keystore, workspace and repository ids
// in the project, maxim package), finished runs' traces are sent there
// automatically through the OTLP Maxim preset, and every run page links to
// its trace in Maxim.
//
//	GET    /integrations/maxim          the connection (never the key)
//	PUT    /integrations/maxim          save it (admin)
//	DELETE /integrations/maxim          disconnect (admin)
//	POST   /integrations/maxim/test     check the key, workspace and repository (admin)
//	GET    /runs/{id}/maxim             the run's push status and link
//	POST   /runs/{id}/maxim/push        send the run now (member)

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/valyala/fasthttp"
)

// MaximClient is the client every Maxim call uses; tests point it at a fake.
var MaximClient = maxim.Client{}

const maximPushTimeout = 45 * time.Second

// MaximConnection is GET /integrations/maxim.
type MaximConnection struct {
	Connected    bool       `json:"connected"`
	WorkspaceID  string     `json:"workspace_id"`
	RepoID       string     `json:"repo_id"`
	AutoPush     bool       `json:"auto_push"`
	LinkTemplate string     `json:"link_template"`
	KeyLast4     string     `json:"key_last4,omitempty"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	Pushed       int        `json:"pushed"`
	Failed       int        `json:"failed"`
	LastError    string     `json:"last_error,omitempty"`
}

// MaximConnectionPut is PUT /integrations/maxim. An empty APIKey keeps the
// stored key.
type MaximConnectionPut struct {
	APIKey       string `json:"api_key,omitempty"`
	WorkspaceID  string `json:"workspace_id"`
	RepoID       string `json:"repo_id"`
	AutoPush     bool   `json:"auto_push"`
	LinkTemplate string `json:"link_template,omitempty"`
}

// MaximTestResult is POST /integrations/maxim/test.
type MaximTestResult struct {
	OK       bool   `json:"ok"`
	Message  string `json:"message"`
	RepoName string `json:"repo_name,omitempty"`
}

// RunMaxim is GET /runs/{id}/maxim.
type RunMaxim struct {
	Connected bool       `json:"connected"`
	Status    string     `json:"status"` // pushed | failed | not_pushed
	TraceID   string     `json:"trace_id"`
	URL       string     `json:"url,omitempty"`
	Error     string     `json:"error,omitempty"`
	PushedAt  *time.Time `json:"pushed_at,omitempty"`
}

type maximHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("maxim", func(env OrgEnv, r *router.Router) {
		h := &maximHandler{env: env}
		r.GET(APIPrefix+"/integrations/maxim", env.R(h.get))
		r.PUT(APIPrefix+"/integrations/maxim", env.W(h.put))
		r.DELETE(APIPrefix+"/integrations/maxim", env.W(h.remove))
		r.POST(APIPrefix+"/integrations/maxim/test", env.W(h.test))
		r.GET(APIPrefix+"/runs/{id}/maxim", env.R(h.runGet))
		r.POST(APIPrefix+"/runs/{id}/maxim/push", env.W(h.runPush))
	})
}

// MaximPushSealed sends one sealed run of an org to its Maxim repository
// when the org is connected; with auto set (the seal hook) only when the
// connection sends finished runs automatically. It returns nil and no error
// when nothing was to be sent. The run must verify against its seal.
func MaximPushSealed(ctx context.Context, svc Services, projectRoot, runID string, auto bool) (*maxim.Push, error) {
	if projectRoot == "" || svc.Index == nil || svc.Keys == nil {
		return nil, nil
	}
	st := maxim.Open(projectRoot)
	s, err := st.Load()
	if err != nil || s == nil || (auto && !s.AutoPush) {
		return nil, err
	}
	rv, ok := svc.Index.Run(runID)
	if !ok {
		return nil, schemas.ErrNotFound
	}
	if !rv.Sealed {
		return nil, errors.New("run " + runID + " is not sealed: only sealed runs are sent")
	}
	if sv, ok := svc.Index.(SealVerifier); ok {
		if err := sv.VerifySeal(ctx, runID); err != nil {
			return nil, err
		}
	}
	m, ok := svc.Index.Manifest(rv.Slot.ExpID)
	if ok && m != nil && containsString(m.HeldOutTasks, rv.Slot.Task) {
		return nil, nil // a held-out task's trace never leaves Heimdall
	}
	key, err := svc.Keys.Reveal(ctx, maximOrg(svc), s.KeyID)
	if err != nil {
		return nil, err
	}
	in := maxim.RunInput{RunID: runID, Slot: rv.Slot, SealHash: rv.SealHash, Auto: auto,
		Files: func(name string) ([]byte, error) { return svc.Index.RunFile(runID, name) }}
	p, err := maxim.PushRun(ctx, MaximClient, st, *s, key, in, time.Now())
	return &p, err
}

func (h *maximHandler) ready(ctx *fasthttp.RequestCtx) (*maxim.Store, keystore.Store, string, bool) {
	switch {
	case h.env.Cfg == nil || h.env.Cfg.Root == "":
		sendUnavailable(ctx, h.env.Mode, "maxim integration (no project folder)")
	case h.env.Services.Keys == nil:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"key store is not configured: the server needs a master key (HEIMDALL_MASTER_KEY) to keep a Maxim API key; ask your server owner")
	default:
		return maxim.Open(h.env.Cfg.Root), h.env.Services.Keys, maximOrg(h.env.Services), true
	}
	return nil, nil, "", false
}

func (h *maximHandler) view(ctx context.Context, st *maxim.Store, ks keystore.Store, org string) (MaximConnection, error) {
	out := MaximConnection{LinkTemplate: maxim.DefaultLinkTemplate}
	s, err := st.Load()
	if err != nil {
		return out, err
	}
	pushes, pushed, failed, err := st.Pushes()
	if err != nil {
		return out, err
	}
	out.Pushed, out.Failed, out.LastError = pushed, failed, maxim.LastError(pushes)
	if s == nil {
		return out, nil
	}
	out.Connected, out.WorkspaceID, out.RepoID, out.AutoPush = true, s.WorkspaceID, s.RepoID, s.AutoPush
	if s.LinkTemplate != "" {
		out.LinkTemplate = s.LinkTemplate
	}
	if !s.UpdatedAt.IsZero() {
		t := s.UpdatedAt
		out.UpdatedAt = &t
	}
	if k, err := ks.Get(ctx, org, s.KeyID); err == nil && k != nil {
		out.KeyLast4 = k.Last4
	}
	return out, nil
}

func (h *maximHandler) get(ctx *fasthttp.RequestCtx) {
	st, ks, org, ok := h.ready(ctx)
	if !ok {
		return
	}
	out, err := h.view(ctx, st, ks, org)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *maximHandler) put(ctx *fasthttp.RequestCtx) {
	st, ks, org, ok := h.ready(ctx)
	if !ok {
		return
	}
	var in MaximConnectionPut
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be a Maxim connection: "+err.Error())
		return
	}
	prev, err := st.Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	s := maxim.Settings{WorkspaceID: strings.TrimSpace(in.WorkspaceID), RepoID: strings.TrimSpace(in.RepoID), AutoPush: in.AutoPush,
		LinkTemplate: strings.TrimSpace(in.LinkTemplate), UpdatedAt: time.Now().UTC(), UpdatedBy: secretActor(ctx)}
	if err := s.Validate(); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, strings.TrimPrefix(err.Error(), maxim.ErrInvalid.Error()+": "))
		return
	}
	key := strings.TrimSpace(in.APIKey)
	if prev != nil {
		s.KeyID = prev.KeyID
	}
	switch {
	case key == "" && s.KeyID == "":
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the Maxim API key is required")
		return
	case key != "" && s.KeyID != "":
		if _, err := ks.Update(ctx, org, s.KeyID, keystore.Put{Label: maxim.KeyLabel, Value: key}, maxim.KeyCreatedBy); err != nil {
			if !errors.Is(err, keystore.ErrNotFound) {
				h.sendKeyErr(ctx, err)
				return
			}
			s.KeyID = "" // the stored key is gone: create it again
		}
	}
	if key != "" && s.KeyID == "" {
		k, err := ks.Create(ctx, org, keystore.KindSecret, keystore.Put{Label: maxim.KeyLabel, Value: key}, maxim.KeyCreatedBy)
		if err != nil {
			h.sendKeyErr(ctx, err)
			return
		}
		s.KeyID = k.ID
	}
	if err := st.Save(s); err != nil {
		sendDepError(ctx, h.env.Mode, "save maxim connection", err)
		return
	}
	out, err := h.view(ctx, st, ks, org)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *maximHandler) sendKeyErr(ctx *fasthttp.RequestCtx, err error) {
	if errors.Is(err, keystore.ErrInvalid) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, strings.TrimPrefix(err.Error(), keystore.ErrInvalid.Error()+": "))
		return
	}
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "store the Maxim API key failed")
}

func (h *maximHandler) remove(ctx *fasthttp.RequestCtx) {
	st, ks, org, ok := h.ready(ctx)
	if !ok {
		return
	}
	s, err := st.Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	if s != nil && s.KeyID != "" {
		if err := ks.Delete(ctx, org, s.KeyID, secretActor(ctx)); err != nil && !errors.Is(err, keystore.ErrNotFound) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "delete the Maxim API key failed")
			return
		}
	}
	if err := st.Delete(); err != nil {
		sendDepError(ctx, h.env.Mode, "delete maxim connection", err)
		return
	}
	out, err := h.view(ctx, st, ks, org)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *maximHandler) test(ctx *fasthttp.RequestCtx) {
	st, ks, org, ok := h.ready(ctx)
	if !ok {
		return
	}
	s, err := st.Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	if s == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Maxim is not connected; save the connection first")
		return
	}
	key, err := ks.Reveal(ctx, org, s.KeyID)
	if err != nil {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, MaximTestResult{Message: "the stored Maxim API key cannot be read; save it again"})
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	repo, err := MaximClient.Test(work, key, s.WorkspaceID, s.RepoID)
	if err != nil {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, MaximTestResult{Message: err.Error()})
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, MaximTestResult{OK: true, RepoName: repo.Name, Message: "Connected to log repository " + repo.Name})
}

func (h *maximHandler) runView(runID string) (RunMaxim, error) {
	out := RunMaxim{Status: maxim.StatusNotPushed, TraceID: traceexport.TraceID(runID)}
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		return out, nil
	}
	st := maxim.Open(h.env.Cfg.Root)
	s, err := st.Load()
	if err != nil || s == nil {
		return out, err
	}
	out.Connected = true
	pushes, _, _, err := st.Pushes()
	if err != nil {
		return out, err
	}
	if p, ok := pushes[runID]; ok {
		out.Status, out.Error = p.Status, p.Error
		at := p.At
		out.PushedAt = &at
		if p.Status == maxim.StatusPushed {
			out.URL = s.Link(p.TraceID)
		}
	}
	return out, nil
}

func (h *maximHandler) runGet(ctx *fasthttp.RequestCtx) {
	runID, ok := idParam(ctx, h.env.Mode, "id", "run")
	if !ok {
		return
	}
	if ix := h.env.Services.Index; ix != nil {
		if _, found := ix.Run(runID); !found {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "run "+runID+" not found")
			return
		}
	}
	out, err := h.runView(runID)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim run", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *maximHandler) runPush(ctx *fasthttp.RequestCtx) {
	runID, ok := idParam(ctx, h.env.Mode, "id", "run")
	if !ok {
		return
	}
	if _, _, _, ok := h.ready(ctx); !ok {
		return
	}
	if h.env.Services.Index == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	}
	work, cancel := context.WithTimeout(context.Background(), maximPushTimeout)
	defer cancel()
	p, err := MaximPushSealed(work, h.env.Services, h.env.Cfg.Root, runID, false)
	switch {
	case errors.Is(err, schemas.ErrNotFound):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "run "+runID+" not found")
		return
	case errors.Is(err, schemas.ErrSealMismatch):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+runID+" does not match its seal and is not sent: "+err.Error())
		return
	case p == nil && err != nil:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	case p == nil:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Maxim is not connected, or the run's task is held out")
		return
	}
	out, verr := h.runView(runID)
	if verr != nil {
		sendDepError(ctx, h.env.Mode, "maxim run", verr)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// maximOrg is the services' org, or the local org.
func maximOrg(s Services) string {
	if s.OrgID != "" {
		return s.OrgID
	}
	return auth.LocalOrgID
}

func containsString(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
