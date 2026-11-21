package handlers

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// F4 key delivery (lane L05): GET /workers/leases/{lease_id}/credentials
// hands a hosted runner the provider keys of the organization whose lease it
// holds. Only a runner token of that organization, holding that lease, gets
// them; users (even owners) never read a key value back over HTTP.

// LeaseHolders answers who holds a coordinator lease (engine.Coordinator).
type LeaseHolders interface {
	LeaseHolder(leaseID string) (schemas.LeaseHolder, bool)
}

var (
	leaseHoldersMu  sync.RWMutex
	leaseHoldersFor func(orgID string) LeaseHolders
)

// SetLeaseHolders installs how the credentials route finds an organization's
// coordinator. The hosted serve sets it from its runner hub; without it the
// route answers 503 and runners keep their own environment keys.
func SetLeaseHolders(f func(orgID string) LeaseHolders) {
	leaseHoldersMu.Lock()
	defer leaseHoldersMu.Unlock()
	leaseHoldersFor = f
}

func leaseHolders(orgID string) LeaseHolders {
	leaseHoldersMu.RLock()
	defer leaseHoldersMu.RUnlock()
	if leaseHoldersFor == nil {
		return nil
	}
	return leaseHoldersFor(orgID)
}

// LeaseCredentialsPath is the route pattern.
const LeaseCredentialsPath = APIPrefix + "/workers/leases/{lease_id}/credentials"

// KeySource is the slice of keystore.Store the route needs.
type KeySource interface {
	List(ctx context.Context, orgID string, k keystore.Kind) ([]keystore.Key, error)
	Reveal(ctx context.Context, orgID, id string) (string, error)
}

type leaseCredsHandler struct {
	mode    schemas.Mode
	orgID   string
	keys    KeySource
	holders func(orgID string) LeaseHolders
	now     func() time.Time
}

func init() {
	RegisterOrgArea("workercreds", func(env OrgEnv, r *router.Router) {
		h := &leaseCredsHandler{mode: env.Mode, orgID: env.Services.OrgID, holders: leaseHolders, now: time.Now}
		if env.Services.Keys != nil {
			h.keys = env.Services.Keys
		}
		r.GET(LeaseCredentialsPath, env.R(h.get))
	})
}

// usableKey reports whether a provider key may be handed to a run: not
// disabled or failing its test, and under its monthly budget.
func usableKey(k keystore.Key) bool {
	switch strings.ToLower(k.Status) {
	case "disabled", "failed", "invalid", "revoked":
		return false
	}
	return k.BudgetUSD <= 0 || k.MonthSpentUSD < k.BudgetUSD
}

func (h *leaseCredsHandler) get(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	p := auth.PrincipalFrom(ctx)
	if p == nil || p.Kind != auth.KindRunner {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"only a runner token (hmr_...) can fetch lease credentials")
		return
	}
	if h.orgID == "" || p.OrgID != h.orgID {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"the runner token belongs to another organization")
		return
	}
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	if h.keys == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"model keys are not configured on this server (set HEIMDALL_MASTER_KEY); runners use their own provider keys")
		return
	}
	var lh LeaseHolders
	if h.holders != nil {
		lh = h.holders(h.orgID)
	}
	if lh == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"no runner coordinator for this organization; runners use their own provider keys")
		return
	}
	holder, ok := lh.LeaseHolder(leaseID)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "lease "+leaseID+" is unknown or has ended")
		return
	}
	if (holder.OrgID != "" && holder.OrgID != h.orgID) || (holder.TokenID != "" && holder.TokenID != p.TokenID) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "the lease is held by another runner")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	creds, err := UsableProviderCredentials(work, h.keys, h.orgID) // G10: every usable key under budget, all of a provider's keys
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list the organization's model keys failed")
		return
	}
	out := schemas.LeaseCredentials{OrgID: h.orgID, LeaseID: leaseID, ExpID: holder.ExpID,
		Credentials: creds, IssuedAt: h.now().UTC()}
	if len(holder.SecretNames) > 0 { // G01: the org secrets this slot names, and no others
		secrets, err := h.leaseSecrets(work, holder.SecretNames)
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read the organization's secrets failed")
			return
		}
		out.Secrets = secrets
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// leaseSecrets reveals the org secrets named in names (keystore kind
// secret, name in Label). A name the org has no secret for is left out: the
// runner's run then fails naming it and pointing to Settings > Secrets.
func (h *leaseCredsHandler) leaseSecrets(ctx context.Context, names []string) (map[string]string, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	keys, err := h.keys.List(ctx, h.orgID, keystore.KindSecret)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, k := range keys {
		if !want[k.Label] || out[k.Label] != "" || (k.Kind != "" && k.Kind != keystore.KindSecret) {
			continue
		}
		v, err := h.keys.Reveal(ctx, h.orgID, k.ID)
		if err != nil || v == "" {
			continue // never echoed
		}
		out[k.Label] = v
	}
	return out, nil
}
