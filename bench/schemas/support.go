package schemas

// W3 customer support (docs/customer-support.md): the support scorecard an
// experiment page shows, GET /experiments/{id}/support.

// SupportSummary is one experiment's support scorecard per arm, over its
// sealed counted runs of one condition. On a support suite verified success
// is called resolution and cost per verified success cost per resolution.
type SupportSummary struct {
	ExpID     string `json:"exp_id"`
	Suite     string `json:"suite"`
	Condition string `json:"condition"`
	// Support is true for a support suite or a suite with a simulated user;
	// otherwise the scorecard does not apply and Arms is empty.
	Support bool `json:"support"`
	// SimulatorMode is scripted or llm (empty for an older plan); the label
	// is "scripted user" or "simulated user (model: provider/model)", and
	// results under two labels are never compared.
	SimulatorMode  string       `json:"simulator_mode,omitempty"`
	SimulatorLabel string       `json:"simulator_label"`
	Synthetic      bool         `json:"synthetic"` // a smoke experiment: never findings
	Arms           []SupportArm `json:"arms"`
}

// SupportArm is one arm's support numbers. A nil number is unknown (no
// decided run, or a run whose value was not observed).
type SupportArm struct {
	Arm   string `json:"arm"`
	Label string `json:"label,omitempty"`
	Runs  int    `json:"runs"`
	// Resolution is the share of decided runs that verified (Resolved of
	// Decided).
	Resolution        *float64 `json:"resolution"`
	Resolved          int      `json:"resolved"`
	Decided           int      `json:"decided"`
	CostPerResolution *float64 `json:"cost_per_resolution"` // agent cost of every run over resolved runs
	// Rates and means over runs where the metric was observed.
	EscalationRate      *float64 `json:"escalation_rate"`
	EscalationCorrect   *float64 `json:"escalation_correct"`
	PolicyViolationRate *float64 `json:"policy_violation_rate"`
	UserTurns           *float64 `json:"user_turns"`
	SimulatorCostUSD    *float64 `json:"simulator_cost_usd"` // total; never part of the agent's cost
}
