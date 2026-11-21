package handlers

// Models and keys area (web parity L04): the provider catalog the model
// picker reads, CRUD over the org's provider keys (keystore Kind provider),
// and a test call per key. A key's value is accepted on create and update
// and never returned: responses carry only its last 4 characters.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

func init() {
	RegisterOrgArea("models", func(env OrgEnv, r *router.Router) {
		h := &modelsHandler{env: env, test: testProviderKey}
		r.GET(APIPrefix+"/models", env.R(h.catalog))
		r.GET(APIPrefix+"/models/keys", env.R(h.list))
		r.POST(APIPrefix+"/models/keys", env.W(h.create))
		r.GET(APIPrefix+"/models/keys/{id}", env.R(h.get))
		r.PUT(APIPrefix+"/models/keys/{id}", env.W(h.update))
		r.DELETE(APIPrefix+"/models/keys/{id}", env.W(h.remove))
		r.POST(APIPrefix+"/models/keys/{id}/test", env.W(h.testKey))
	})
}

// ModelKeyView is a provider key as the API shows it: never the value.
type ModelKeyView struct {
	ID            string     `json:"id"`
	Provider      string     `json:"provider"`
	Label         string     `json:"label"`
	BaseURL       string     `json:"base_url,omitempty"`
	Last4         string     `json:"last4"`
	Status        string     `json:"status"`
	Models        []string   `json:"models"`
	BudgetUSD     float64    `json:"budget_usd"`
	MonthSpentUSD float64    `json:"month_spent_usd"`
	OverBudget    bool       `json:"over_budget"`
	CreatedBy     string     `json:"created_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	LastTestedAt  *time.Time `json:"last_tested_at,omitempty"`
}

// ModelKeyRequest creates or updates a provider key. On update an empty
// value keeps the stored one.
type ModelKeyRequest struct {
	Provider  string   `json:"provider"`
	Label     string   `json:"label,omitempty"`
	BaseURL   string   `json:"base_url,omitempty"`
	Value     string   `json:"value,omitempty"`
	Models    []string `json:"models,omitempty"`
	BudgetUSD float64  `json:"budget_usd,omitempty"`
}

// ModelKeyResponse wraps one key.
type ModelKeyResponse struct {
	Key ModelKeyView `json:"key"`
}

// ModelKeysResponse lists the org's provider keys.
type ModelKeysResponse struct {
	Keys []ModelKeyView `json:"keys"`
}

// ModelKeyTestResult is the outcome of a key's test call.
type ModelKeyTestResult struct {
	OK         bool     `json:"ok"`
	Status     string   `json:"status"` // ok | invalid | error
	Message    string   `json:"message"`
	HTTPStatus int      `json:"http_status,omitempty"`
	LatencyMS  int64    `json:"latency_ms"`
	Models     []string `json:"models,omitempty"` // model ids the provider listed (at most 200)
}

// ModelKeyTestResponse is the key after the test and the test's outcome.
type ModelKeyTestResponse struct {
	Key    ModelKeyView       `json:"key"`
	Result ModelKeyTestResult `json:"result"`
}

// ModelRefView names a provider, a model and optionally the key to use.
type ModelRefView struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	KeyID    string `json:"key_id,omitempty"`
}

// ModelsCatalogResponse is GET /models: the providers with suggested
// models, the org's keys, and the default model (the cheapest one a working
// key can call).
type ModelsCatalogResponse struct {
	Providers []ModelProviderInfo `json:"providers"`
	Keys      []ModelKeyView      `json:"keys"`
	Default   *ModelRefView       `json:"default,omitempty"`
	KeysReady bool                `json:"keys_ready"` // false: the key store is not configured on this server
}

type modelsHandler struct {
	env  OrgEnv
	test func(ctx context.Context, hosted bool, k *keystore.Key, value string) ModelKeyTestResult
}

func keyView(k keystore.Key) ModelKeyView {
	models := k.Models
	if models == nil {
		models = []string{}
	}
	return ModelKeyView{ID: k.ID, Provider: k.Provider, Label: k.Label, BaseURL: k.BaseURL, Last4: k.Last4, Status: k.Status,
		Models: models, BudgetUSD: k.BudgetUSD, MonthSpentUSD: k.MonthSpentUSD, OverBudget: k.BudgetUSD > 0 && k.MonthSpentUSD >= k.BudgetUSD,
		CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt, LastTestedAt: k.LastTestedAt}
}

const keysUnavailable = "the key store is not configured on this server: set HEIMDALL_MASTER_KEY (base64 of 32 random bytes, e.g. `openssl rand -base64 32`) and a database, then restart"

func (h *modelsHandler) store(ctx *fasthttp.RequestCtx) keystore.Store {
	if h.env.Services.Keys == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, keysUnavailable)
		return nil
	}
	return h.env.Services.Keys
}

func (h *modelsHandler) orgID(ctx *fasthttp.RequestCtx) string {
	if h.env.Services.OrgID != "" {
		return h.env.Services.OrgID
	}
	if p := auth.PrincipalFrom(ctx); p != nil && p.OrgID != "" {
		return p.OrgID
	}
	return auth.LocalOrgID
}

func modelsActor(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	if p == nil {
		return ""
	}
	return p.ActorID()
}

// keyError answers a keystore error with the matching status. Store errors
// carry no secret material.
func (h *modelsHandler) keyError(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, keystore.ErrNotFound):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "key not found: it may have been deleted; reload the list")
	case errors.Is(err, keystore.ErrInvalid):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, strings.TrimPrefix(err.Error(), "keystore: invalid key: "))
	case errors.Is(err, keystore.ErrNoMasterKey):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, keysUnavailable)
	default:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "key store error; check the server log")
	}
}

func (h *modelsHandler) listKeys(ctx *fasthttp.RequestCtx, s keystore.Store) ([]ModelKeyView, error) {
	keys, err := s.List(ctx, h.orgID(ctx), keystore.KindProvider)
	if err != nil {
		return nil, err
	}
	out := make([]ModelKeyView, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyView(k))
	}
	return out, nil
}

func (h *modelsHandler) catalog(ctx *fasthttp.RequestCtx) {
	resp := ModelsCatalogResponse{Providers: modelProviders(), Keys: []ModelKeyView{}}
	if s := h.env.Services.Keys; s != nil {
		keys, err := h.listKeys(ctx, s)
		if err != nil {
			h.keyError(ctx, err)
			return
		}
		resp.Keys, resp.KeysReady = keys, true
		resp.Default = defaultModel(keys)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, resp)
}

func (h *modelsHandler) list(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	keys, err := h.listKeys(ctx, s)
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ModelKeysResponse{Keys: keys})
}

func (h *modelsHandler) get(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	k, err := s.Get(ctx, h.orgID(ctx), pathID(ctx))
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ModelKeyResponse{Key: keyView(*k)})
}

func pathID(ctx *fasthttp.RequestCtx) string {
	id, _ := ctx.UserValue("id").(string)
	return id
}

func (h *modelsHandler) decode(ctx *fasthttp.RequestCtx) (keystore.Put, bool) {
	var req ModelKeyRequest
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"body must be JSON {provider, label, base_url, value, models, budget_usd}: "+err.Error())
		return keystore.Put{}, false
	}
	if req.BaseURL != "" && h.env.Services.Hosted {
		if err := checkHostedBaseURL(req.BaseURL); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return keystore.Put{}, false
		}
	}
	return keystore.Put{Provider: req.Provider, Label: req.Label, BaseURL: req.BaseURL, Value: req.Value, Models: req.Models, BudgetUSD: req.BudgetUSD}, true
}

func (h *modelsHandler) create(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	in, ok := h.decode(ctx)
	if !ok {
		return
	}
	k, err := s.Create(ctx, h.orgID(ctx), keystore.KindProvider, in, modelsActor(ctx))
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, ModelKeyResponse{Key: keyView(*k)})
}

func (h *modelsHandler) update(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	in, ok := h.decode(ctx)
	if !ok {
		return
	}
	k, err := s.Update(ctx, h.orgID(ctx), pathID(ctx), in, modelsActor(ctx))
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ModelKeyResponse{Key: keyView(*k)})
}

func (h *modelsHandler) remove(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	if err := s.Delete(ctx, h.orgID(ctx), pathID(ctx), modelsActor(ctx)); err != nil {
		h.keyError(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *modelsHandler) testKey(ctx *fasthttp.RequestCtx) {
	s := h.store(ctx)
	if s == nil {
		return
	}
	org, id := h.orgID(ctx), pathID(ctx)
	k, err := s.Get(ctx, org, id)
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	value, err := s.Reveal(ctx, org, id)
	if err != nil {
		h.keyError(ctx, err)
		return
	}
	tctx, cancel := context.WithTimeout(context.Background(), 20*time.Second) // fasthttp contexts may carry no Done channel
	defer cancel()
	res := h.test(tctx, h.env.Services.Hosted, k, value)
	res.Message = scrubSecret(res.Message, value)
	if err := s.SetStatus(ctx, org, id, res.Status, time.Now()); err != nil {
		h.keyError(ctx, err)
		return
	}
	if k2, err := s.Get(ctx, org, id); err == nil {
		k = k2
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ModelKeyTestResponse{Key: keyView(*k), Result: res})
}

// scrubSecret removes the key value from a message a provider sent back.
func scrubSecret(msg, value string) string {
	if value != "" {
		msg = strings.ReplaceAll(msg, value, "[redacted]")
	}
	return msg
}
