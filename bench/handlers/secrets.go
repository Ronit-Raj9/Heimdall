package handlers

// Org secrets area (web parity lane L06): named secrets an org stores for
// its tools (external API credentials read through auth_env). They live in
// the keystore as Kind "secret" with the name in Label. A value is accepted
// on create and on rotate and is never returned, not even its last four
// characters; only the tool proxy reads it (toolproxy/orgsecrets.go).

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/toolproxy"
	"github.com/valyala/fasthttp"
)

// MaxOrgSecrets caps the secrets one org may store.
const MaxOrgSecrets = 100

// Secret is a stored secret's metadata; the value is never part of it.
type Secret struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by,omitempty"`
	// CreatedByName is how the creator reads (a member's display name);
	// empty when it would only repeat created_by.
	CreatedByName string    `json:"created_by_name,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SecretList is GET /secrets.
type SecretList struct {
	Secrets []Secret `json:"secrets"`
}

// SecretCreate is POST /secrets.
type SecretCreate struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SecretRotate is PUT /secrets/{id}: a new value.
type SecretRotate struct {
	Value string `json:"value"`
}

// SecretDeleted is DELETE /secrets/{id}.
type SecretDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type secretsHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("secrets", func(env OrgEnv, r *router.Router) {
		h := &secretsHandler{env: env}
		r.GET(APIPrefix+"/secrets", env.R(h.list))
		r.POST(APIPrefix+"/secrets", env.W(h.create))
		r.PUT(APIPrefix+"/secrets/{id}", env.W(h.rotate))
		r.DELETE(APIPrefix+"/secrets/{id}", env.W(h.remove))
	})
}

func secretView(k keystore.Key) Secret {
	return Secret{ID: k.ID, Name: k.Label, CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt, UpdatedAt: k.UpdatedAt}
}

// store returns the keystore and the org, or answers 503 and returns nil.
func (h *secretsHandler) store(ctx *fasthttp.RequestCtx) (keystore.Store, string) {
	ks := h.env.Services.Keys
	if ks == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"secrets store is not configured: the server needs a master key (HEIMDALL_MASTER_KEY) and a key store; ask your server owner")
		return nil, ""
	}
	org := h.env.Services.OrgID
	if org == "" {
		org = auth.PrincipalFrom(ctx).OrgID
	}
	if org == "" {
		org = auth.LocalOrgID
	}
	return ks, org
}

func secretActor(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	switch {
	case p.Email != "":
		return p.Email
	case p.UserID != "":
		return p.UserID
	}
	return string(p.Kind)
}

func (h *secretsHandler) list(ctx *fasthttp.RequestCtx) {
	ks, org := h.store(ctx)
	if ks == nil {
		return
	}
	keys, err := ks.List(ctx, org, keystore.KindSecret)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list secrets failed")
		return
	}
	out := SecretList{Secrets: make([]Secret, 0, len(keys))}
	names := newActorNames(ctx)
	for _, k := range keys {
		if systemSecret(k) {
			continue // the server's own credentials (shared runner token) are not the org's to rotate or delete
		}
		v := secretView(k)
		v.CreatedBy, v.CreatedByName = names.show(v.CreatedBy)
		out.Secrets = append(out.Secrets, v)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// checkSecretValue reports why v cannot be a secret value, or "".
func checkSecretValue(v string) string {
	switch {
	case v == "":
		return "value is required"
	case len(v) > toolproxy.MaxOrgSecretBytes:
		return "value is too large (max 16 KiB)"
	case strings.ContainsRune(v, 0):
		return "value must not contain NUL bytes"
	}
	return ""
}

func (h *secretsHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *secretsHandler) create(ctx *fasthttp.RequestCtx) {
	ks, org := h.store(ctx)
	if ks == nil {
		return
	}
	var in SecretCreate
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		h.bad(ctx, `body must be JSON: {"name": "TAVILY_API_KEY", "value": "..."}`)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := toolproxy.CheckOrgSecretName(in.Name); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if msg := checkSecretValue(in.Value); msg != "" {
		h.bad(ctx, msg)
		return
	}
	keys, err := ks.List(ctx, org, keystore.KindSecret)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list secrets failed")
		return
	}
	for _, k := range keys {
		if k.Label == in.Name {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				"a secret named "+in.Name+" already exists: rotate it to change its value")
			return
		}
	}
	if len(keys) >= MaxOrgSecrets {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this org already has the maximum of 100 secrets: delete one first")
		return
	}
	k, err := ks.Create(ctx, org, keystore.KindSecret, keystore.Put{Provider: "secret", Label: in.Name, Value: in.Value}, secretActor(ctx))
	if err != nil {
		h.storeError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, secretView(*k))
}

// secretByID loads id and checks it is a secret of org (a provider key id
// answers 404, so this area can never touch model keys).
func (h *secretsHandler) secretByID(ctx *fasthttp.RequestCtx, ks keystore.Store, org string) *keystore.Key {
	id, _ := ctx.UserValue("id").(string)
	k, err := ks.Get(ctx, org, id)
	if err != nil || k == nil || k.Kind != keystore.KindSecret || systemSecret(*k) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "secret not found: "+id)
		return nil
	}
	return k
}

func (h *secretsHandler) rotate(ctx *fasthttp.RequestCtx) {
	ks, org := h.store(ctx)
	if ks == nil {
		return
	}
	var in SecretRotate
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		h.bad(ctx, `body must be JSON: {"value": "..."}`)
		return
	}
	if msg := checkSecretValue(in.Value); msg != "" {
		h.bad(ctx, msg)
		return
	}
	k := h.secretByID(ctx, ks, org)
	if k == nil {
		return
	}
	put := keystore.Put{Provider: k.Provider, Label: k.Label, BaseURL: k.BaseURL, Models: k.Models, BudgetUSD: k.BudgetUSD, Value: in.Value}
	nk, err := ks.Update(ctx, org, k.ID, put, secretActor(ctx))
	if err != nil {
		h.storeError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, secretView(*nk))
}

func (h *secretsHandler) remove(ctx *fasthttp.RequestCtx) {
	ks, org := h.store(ctx)
	if ks == nil {
		return
	}
	k := h.secretByID(ctx, ks, org)
	if k == nil {
		return
	}
	if err := ks.Delete(ctx, org, k.ID, secretActor(ctx)); err != nil {
		h.storeError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, SecretDeleted{ID: k.ID, Deleted: true})
}

// storeError maps keystore errors without ever echoing a value.
func (h *secretsHandler) storeError(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, keystore.ErrNotFound):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "secret not found")
	case errors.Is(err, keystore.ErrInvalid):
		h.bad(ctx, "invalid secret: name and value are required")
	case errors.Is(err, keystore.ErrNoMasterKey):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "secrets store has no master key: ask your server owner to set HEIMDALL_MASTER_KEY")
	default:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "secrets store error")
	}
}

// systemSecret reports a secret the server keeps for itself in the org's
// keystore (created by "heimdall:...", e.g. the shared runner's token).
func systemSecret(k keystore.Key) bool { return strings.HasPrefix(k.CreatedBy, "heimdall:") }
