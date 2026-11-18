package schemas

// W4 research with Tavily (docs/research-tasks.md): the research scorecard
// an experiment page shows, GET /experiments/{id}/research.

// ResearchSummary is one experiment's research scorecard per arm, over its
// sealed counted runs of one condition.
type ResearchSummary struct {
	ExpID     string `json:"exp_id"`
	Suite     string `json:"suite"`
	Condition string `json:"condition"`
	// Research is true when a run reported the research metrics (a research
	// suite or a task with a Tavily API); otherwise Arms is empty.
	Research bool `json:"research"`
	// Replay is false when the experiment called Tavily live or recorded
	// (external: live | record): its search results are not repeatable.
	Replay    bool          `json:"replay"`
	Synthetic bool          `json:"synthetic"` // a smoke experiment: never findings
	Arms      []ResearchArm `json:"arms"`
}

// ResearchArm is one arm's research numbers. A nil number is unknown or not
// applicable in every run of the arm.
type ResearchArm struct {
	Arm   string `json:"arm"`
	Label string `json:"label,omitempty"`
	Runs  int    `json:"runs"`
	// Verified is the share of decided runs that verified (Passed of
	// Decided): a right answer with acceptable sources.
	Verified *float64 `json:"verified"`
	Passed   int      `json:"passed"`
	Decided  int      `json:"decided"`
	// Means over the runs where the metric was observed.
	Searches          *float64 `json:"searches"`
	SourcesCited      *float64 `json:"sources_cited"`
	CitationsGrounded *float64 `json:"citations_grounded"`
	CitationPrecision *float64 `json:"citation_precision"`
	Unmatched         *float64 `json:"external_unmatched"` // replayed searches the cassette did not hold
}
