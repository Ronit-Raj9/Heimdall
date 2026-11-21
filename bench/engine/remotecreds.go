package engine

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
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

// OrgLogPolicyGateway is a gateway that tags an organization's keys and
// applies its prompt logging policy (gateways/bifrost/orglogs.go; lane X03).
type OrgLogPolicyGateway interface {
	SetExperimentLogPolicy(expID, orgID string, p schemas.PromptLogPolicy)
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
		if l.r != nil { // G01: only the secrets this slot's runs name
			out.SecretNames = utils.SlotSecretNames(l.r.m, l.r.suite, l.slot)
		}
	}
	if w, ok := c.workers[l.worker]; ok && w != nil {
		out.TokenID = w.scope.TokenID
	}
	return out, true
}

// applyLeaseCredentials fetches the lease's organization keys and applies
// them to the runner's gateway, and returns the org secrets the lease
// delivered (G01; nil when the server delivered none). It returns a refusal
// reason when the coordinator refused the runner (another organization's
// lease) or the keys could not be applied.
//
// hosted is a lease of a hosted organization (the manifest defers its
// gateway to the runner). Its runs never fall back to this runner's own
// provider keys (X01): a lease whose keys cannot be fetched is refused, and
// the experiment is bound to the organization even when it has no key, so
// the gateway refuses a paid run on a provider the organization has no key
// for. A local lease (hosted false) keeps the runner-wide keys when the
// server delivers none, as before.
func (w *Worker) applyLeaseCredentials(ctx context.Context, lease *schemas.WorkerLease, hosted bool) (map[string]string, string) {
	var gw OrgCredentialGateway
	var lg OrgLogPolicyGateway
	if w.reg != nil && w.reg.Gateway != nil {
		gw, _ = w.reg.Gateway.(OrgCredentialGateway)
		lg, _ = w.reg.Gateway.(OrgLogPolicyGateway)
	}
	var out schemas.LeaseCredentials
	err := w.call(ctx, http.MethodGet, workerAPI+"/leases/"+url.PathEscape(lease.LeaseID)+"/credentials", nil, &out)
	switch {
	case errors.Is(err, schemas.ErrWorkerScope) || errors.Is(err, schemas.ErrWorkerAuth):
		return nil, "the coordinator refused the lease's credentials: " + err.Error()
	case err != nil && hosted:
		return nil, "could not fetch the organization's model keys for the lease (" + err.Error() + "); runs never use this runner's own provider keys"
	case err != nil:
		w.logger.Info("no organization keys for the lease; using this runner's own provider keys", "lease", lease.LeaseID, "reason", err.Error())
		return nil, ""
	}
	secrets := out.Secrets
	if secrets == nil {
		secrets = map[string]string{} // the server answered: its org secrets are the run's only source
	}
	if lg != nil && out.PromptLogging != nil && out.OrgID != "" && lease.ExpID != "" {
		lg.SetExperimentLogPolicy(lease.ExpID, out.OrgID, out.PromptLogging.Normalize()) // X03: before any key of the lease is minted
	}
	if gw == nil {
		if hosted && w.reg != nil && w.reg.Gateway != nil {
			return nil, "this runner's gateway cannot serve organization model keys; runs never use this runner's own provider keys"
		}
		return secrets, ""
	}
	if out.OrgID == "" {
		if hosted {
			return nil, "the server named no organization for the lease's model keys"
		}
		return secrets, ""
	}
	if len(out.Credentials) == 0 {
		if hosted {
			gw.BindExperimentOrg(lease.ExpID, out.OrgID) // X01: no key, no paid run
		}
		return secrets, ""
	}
	if err := gw.ApplyOrgCredentials(ctx, out.OrgID, out.Credentials); err != nil {
		return nil, "apply the organization's provider keys: " + err.Error()
	}
	gw.BindExperimentOrg(lease.ExpID, out.OrgID)
	w.logger.Info("organization provider keys applied", "lease", lease.LeaseID, "org", out.OrgID, "keys", len(out.Credentials))
	return secrets, ""
}
