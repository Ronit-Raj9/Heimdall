// Package compat checks whether an agent can run a benchmark. This is a
// minimal version for lane A02; lane A01 owns the package and replaces it
// with the capability check of contract section 1.
package compat

import "github.com/maximhq/bifrost/bench/schemas"

// Result is the answer for one agent and one benchmark.
type Result struct {
	OK       bool     `json:"ok"`
	Requires []string `json:"requires"`
	Missing  []string `json:"missing"`
	Reason   string   `json:"reason"`
}

// Check reports whether agent can run suite s. The stub accepts every pair.
func Check(agent *schemas.AgentConfig, s *schemas.Suite) Result {
	return Result{OK: true, Requires: []string{}, Missing: []string{}}
}
