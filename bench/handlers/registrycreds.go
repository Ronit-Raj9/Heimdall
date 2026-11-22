package handlers

// Private registry logins (lane Y10, gap P0-6): an org stores how the
// shared worker logs in to its private image registries (Docker Hub,
// GitHub, Amazon ECR, Google, Azure, any other). A login names a Settings >
// Secrets entry for its password; the login itself holds no secret, and no
// password is ever returned. The server resolves the logins for each run
// (handlers/workercreds.go) and the worker uses one for a single docker
// pull (bench/registrycreds).
//
//   GET    /api/bench/registry-credentials       org viewer
//   POST   /api/bench/registry-credentials       org admin
//   DELETE /api/bench/registry-credentials/{id}  org admin

import (
	"encoding/json"
	"errors"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/registrycreds"
	"github.com/valyala/fasthttp"
)

// RegistryCredential is one login as the API shows it.
type RegistryCredential struct {
	registrycreds.Login
	// SecretFound is false when the named secret is missing under Settings
	// > Secrets: pulls from this registry fail until it is added.
	SecretFound bool `json:"secret_found"`
}

// RegistryCredentialList is GET /registry-credentials.
type RegistryCredentialList struct {
	Credentials []RegistryCredential `json:"credentials"`
	Kinds       []string             `json:"kinds"`
	Max         int                  `json:"max"`
}

// RegistryCredentialDeleted is DELETE /registry-credentials/{id}.
type RegistryCredentialDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// RegistryCredentialsPath is the collection route.
const RegistryCredentialsPath = APIPrefix + "/registry-credentials"

type registryCredsHandler struct{ s *secretsHandler }

func init() {
	RegisterOrgArea("registrycreds", func(env OrgEnv, r *router.Router) {
		h := &registryCredsHandler{s: &secretsHandler{env: env}}
		r.GET(RegistryCredentialsPath, env.R(h.list))
		r.POST(RegistryCredentialsPath, env.W(h.create))
		r.DELETE(RegistryCredentialsPath+"/{id}", env.W(h.remove))
	})
}

func (h *registryCredsHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	mode := h.s.env.Mode
	switch {
	case errors.Is(err, registrycreds.ErrInvalid):
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, keystore.ErrNotFound):
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "registry login not found")
	case errors.Is(err, keystore.ErrNoMasterKey):
		lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "the key store has no master key: ask your server owner to set HEIMDALL_MASTER_KEY")
	default:
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "registry login store error")
	}
}

func (h *registryCredsHandler) list(ctx *fasthttp.RequestCtx) {
	ks, org := h.s.store(ctx)
	if ks == nil {
		return
	}
	logins, err := registrycreds.List(ctx, ks, org)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	names, err := registrycreds.SecretNames(ctx, ks, org)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	out := RegistryCredentialList{Credentials: make([]RegistryCredential, 0, len(logins)), Kinds: registrycreds.Kinds, Max: registrycreds.MaxPerOrg}
	for _, l := range logins {
		out.Credentials = append(out.Credentials, RegistryCredential{Login: l, SecretFound: names[l.SecretName]})
	}
	lib.SendJSON(ctx, h.s.env.Mode, fasthttp.StatusOK, out)
}

func (h *registryCredsHandler) create(ctx *fasthttp.RequestCtx) {
	ks, org := h.s.store(ctx)
	if ks == nil {
		return
	}
	var in registrycreds.Input
	if err := json.Unmarshal(ctx.PostBody(), &in); err != nil {
		h.s.bad(ctx, `body must be JSON: {"kind": "ghcr", "username": "octocat", "secret_name": "GHCR_TOKEN"}`)
		return
	}
	in, err := registrycreds.Normalize(in)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	logins, err := registrycreds.List(ctx, ks, org)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	for _, l := range logins {
		if l.Host == in.Host {
			lib.SendError(ctx, h.s.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				"a login for "+in.Host+" already exists: remove it first to replace it")
			return
		}
	}
	if len(logins) >= registrycreds.MaxPerOrg {
		lib.SendError(ctx, h.s.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this org already has the maximum of 20 registry logins: remove one first")
		return
	}
	put, err := registrycreds.Put(in)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	k, err := ks.Create(ctx, org, keystore.KindRegistry, put, secretActor(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	l, err := registrycreds.FromKey(ctx, ks, *k)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	names, _ := registrycreds.SecretNames(ctx, ks, org)
	lib.SendJSON(ctx, h.s.env.Mode, fasthttp.StatusCreated, RegistryCredential{Login: l, SecretFound: names[l.SecretName]})
}

func (h *registryCredsHandler) remove(ctx *fasthttp.RequestCtx) {
	ks, org := h.s.store(ctx)
	if ks == nil {
		return
	}
	id, _ := ctx.UserValue("id").(string)
	k, err := ks.Get(ctx, org, id)
	if err != nil || k == nil || k.Kind != keystore.KindRegistry {
		lib.SendError(ctx, h.s.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "registry login not found: "+id)
		return
	}
	if err := ks.Delete(ctx, org, k.ID, secretActor(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.s.env.Mode, fasthttp.StatusOK, RegistryCredentialDeleted{ID: k.ID, Deleted: true})
}
