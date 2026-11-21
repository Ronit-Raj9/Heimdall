package engine

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/maximhq/bifrost/bench/schemas"
)

// F4 key delivery (lane L05): a hosted runner fetches its organization's
// provider keys for each lease and hands them to its gateway, so every model
// call of the lease (agent, judges, simulated users, voice) goes agent,
// Bifrost, fault proxy, provider on the organization's own keys.

// OrgCredentialGateway is a gateway that can serve an organization's own
// provider keys (gateways/bifrost/orgcreds.go). A gateway without it keeps
// the runner-wide keys from its environment.
type OrgCredentialGateway interface {
	// ApplyOrgCredentials registers the organization's keys with the gateway
	// (idempotent; an empty list removes nothing).
	ApplyOrgCredentials(ctx context.Context, orgID string, creds []schemas.ProviderCredential) error
	// BindExperimentOrg routes the experiment's run keys to the
	// organization's keys for the providers it registered.
	BindExperimentOrg(expID, orgID string)
}

// LeaseHolder reports who holds a live lease of this coordinator: the
// worker, its runner token (hosted) and the experiment. ok is false for an
// unknown or ended lease.
func (c *Coordinator) LeaseHolder(leaseID string) (schemas.LeaseHolder, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.leases[leaseID]
	if !ok || l == nil {
		return schemas.LeaseHolder{}, false
	}
	out := schemas.LeaseHolder{WorkerID: l.worker, OrgID: c.opts.OrgID}
	if l.slot != nil {
		out.ExpID = l.slot.ExpID
	}
	if w, ok := c.workers[l.worker]; ok && w != nil {
		out.TokenID = w.scope.TokenID
	}
	return out, true
}

// applyLeaseCredentials fetches the lease's organization keys and applies
// them to the runner's gateway. It returns a refusal reason only when the
// coordinator refused the runner (another organization's lease); every other
// failure (a server without key delivery, an org without keys) falls back to
// the runner-wide keys and is logged.
func (w *Worker) applyLeaseCredentials(ctx context.Context, lease *schemas.WorkerLease) string {
	if w.reg == nil || w.reg.Gateway == nil {
		return ""
	}
	gw, ok := w.reg.Gateway.(OrgCredentialGateway)
	if !ok {
		return ""
	}
	var out schemas.LeaseCredentials
	err := w.call(ctx, http.MethodGet, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/credentials", nil, &out)
	switch {
	case errors.Is(err, schemas.ErrWorkerScope) || errors.Is(err, schemas.ErrWorkerAuth):
		return "the coordinator refused the lease's credentials: " + err.Error()
	case err != nil:
		w.logger.Info("no organization keys for the lease; using this runner's own provider keys", "lease", lease.LeaseID, "reason", err.Error())
		return ""
	case len(out.Credentials) == 0 || out.OrgID == "":
		return ""
	}
	if err := gw.ApplyOrgCredentials(ctx, out.OrgID, out.Credentials); err != nil {
		return "apply the organization's provider keys: " + err.Error()
	}
	gw.BindExperimentOrg(lease.ExpID, out.OrgID)
	w.logger.Info("organization provider keys applied", "lease", lease.LeaseID, "org", out.OrgID, "keys", len(out.Credentials))
	return ""
}
