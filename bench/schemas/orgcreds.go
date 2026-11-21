package schemas

import "time"

// ProviderCredential is one of an organization's provider keys as a hosted
// runner receives it for a lease (F4 key delivery, lane L05). Value is the
// plaintext key: it travels only over the runner's authenticated lease
// route, is held in memory by the runner, and is never logged or written to
// a run folder.
type ProviderCredential struct {
	KeyID    string   `json:"key_id"`
	Provider string   `json:"provider"`           // any Bifrost provider (schemas.BifrostProviders), openai-compatible, or a custom OpenAI-compatible name
	BaseURL  string   `json:"base_url,omitempty"` // custom and openai-compatible endpoints, Azure endpoint, Databricks workspace, self-hosted server
	Models   []string `json:"models,omitempty"`   // the models the key may serve; empty = any
	Value    string   `json:"value"`              // the key, or for AWS, Vertex and Cloudflare a JSON object of the provider's fields
}

// LeaseCredentials is the answer of GET /api/bench/workers/leases/{lease_id}/credentials.
type LeaseCredentials struct {
	OrgID       string               `json:"org_id"`
	LeaseID     string               `json:"lease_id"`
	ExpID       string               `json:"exp_id,omitempty"`
	Credentials []ProviderCredential `json:"credentials"`
	// Secrets are the org secrets the leased slot names (external api
	// auth_env, agent env {{secret.NAME}}; gap G01), name to value. Only the
	// names of that slot, only to a runner token of the lease's org. The
	// runner holds them in memory and hands them to the run's tool proxy
	// and agent env; never logged or written to a run folder.
	Secrets map[string]string `json:"secrets,omitempty"`
	// PromptLogging is the organization's gateway prompt logging policy
	// (lane X03): the runner tags the lease's gateway keys with the
	// organization and turns prompt text logging off when it says so. nil
	// from an older server: keys stay untagged, as before.
	PromptLogging *PromptLogPolicy `json:"prompt_logging,omitempty"`
	IssuedAt      time.Time        `json:"issued_at"`
}

// Gateway log row metadata keys a hosted runner stamps on each virtual key
// it mints for an organization's run. Bifrost copies them onto every log row
// of the key, so the server lists and deletes one organization's rows with
// the filter metadata_heimdall_org=<org>.
const (
	PromptLogOrgMetadataKey = "heimdall_org"
	PromptLogRunMetadataKey = "heimdall_run"
)

// Prompt log retention bounds, in days. The shared gateway's own
// log_retention_days (deploy/bifrost/config.json) is the hard ceiling.
const (
	PromptLogRetentionDefault = 7
	PromptLogRetentionMax     = 30
)

// PromptLogPolicy is an organization's gateway prompt logging setting.
type PromptLogPolicy struct {
	// KeepContent keeps the prompt and response text of model calls in the
	// gateway log. Off: the gateway stores token counts, cost and timing
	// only.
	KeepContent bool `json:"keep_content"`
	// RetentionDays is how long the gateway keeps the organization's log
	// rows (1 to PromptLogRetentionMax).
	RetentionDays int `json:"retention_days"`
}

// DefaultPromptLogPolicy is the policy of an organization that never chose
// one: text kept (traces and fault checks read it) for a week.
func DefaultPromptLogPolicy() PromptLogPolicy {
	return PromptLogPolicy{KeepContent: true, RetentionDays: PromptLogRetentionDefault}
}

// Normalize clamps RetentionDays into range; 0 means the default.
func (p PromptLogPolicy) Normalize() PromptLogPolicy {
	switch {
	case p.RetentionDays <= 0:
		p.RetentionDays = PromptLogRetentionDefault
	case p.RetentionDays > PromptLogRetentionMax:
		p.RetentionDays = PromptLogRetentionMax
	}
	return p
}

// LeaseHolder is who holds a coordinator lease: the worker, the runner token
// it registered with (hosted), and the experiment.
type LeaseHolder struct {
	WorkerID string
	TokenID  string
	OrgID    string
	ExpID    string
	// SecretNames are the org secrets the leased slot names (G01): the
	// credentials route reveals these and no others.
	SecretNames []string
}
