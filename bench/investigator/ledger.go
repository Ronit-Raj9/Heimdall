package investigator

// LedgerRow is one model call the runner made, for the job result.
type LedgerRow struct {
	Purpose   string  `json:"purpose"`
	Model     string  `json:"model"`
	Served    string  `json:"served,omitempty"`
	PromptSHA string  `json:"prompt_sha"`
	CostUSD   float64 `json:"cost_usd"`
	Attempts  int     `json:"attempts"`
	At        string  `json:"at"`
	Error     string  `json:"error,omitempty"`
}
