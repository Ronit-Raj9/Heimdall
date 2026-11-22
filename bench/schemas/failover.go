package schemas

// FailoverOption is the "Provider failover" evaluation option (lane C07,
// package failover): Bifrost fallbacks for the agent's model, tested under
// an outage of the primary model. Fallbacks are tried in order; a provider
// left empty is the run's own, and no other provider is allowed (a run key
// holds one provider key slot).
type FailoverOption struct {
	Enabled      bool          `json:"enabled"`
	Fallbacks    []RouteTarget `json:"fallbacks"`
	OutageStatus int           `json:"outage_status,omitempty"` // default 503
}
