package schemas

import "time"

// ProviderCredential is one of an organization's provider keys as a hosted
// runner receives it for a lease (F4 key delivery, lane L05). Value is the
// plaintext key: it travels only over the runner's authenticated lease
// route, is held in memory by the runner, and is never logged or written to
// a run folder.
type ProviderCredential struct {
	KeyID    string   `json:"key_id"`
	Provider string   `json:"provider"`           // openai, anthropic, gemini, openrouter, groq, sarvam, openai-compatible
	BaseURL  string   `json:"base_url,omitempty"` // openai-compatible endpoints only
	Models   []string `json:"models,omitempty"`   // the models the key may serve; empty = any
	Value    string   `json:"value"`
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
	Secrets  map[string]string `json:"secrets,omitempty"`
	IssuedAt time.Time         `json:"issued_at"`
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
