package clusters

import "time"

// ExampleRun is one example run of a cluster with what a page shows for it.
type ExampleRun struct {
	RunID  string    `json:"run_id"`
	PairID string    `json:"pair_id,omitempty"`
	ExpID  string    `json:"exp_id"`
	Suite  string    `json:"suite"`
	Task   string    `json:"task"`
	At     time.Time `json:"at"`
}

func exampleOf(r RunRef) ExampleRun {
	return ExampleRun{RunID: r.RunID, PairID: r.PairID, ExpID: r.ExpID, Suite: r.Suite, Task: r.Task, At: r.At}
}
