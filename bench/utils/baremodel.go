package utils

import "github.com/maximhq/bifrost/bench/schemas"

// Bare-model baseline (G4-21): the shipped agents/bare-model.yaml makes one
// model call with the task text and no tools. It marks itself with
// BareModelEnv, so the compatibility rules (compat.TaskOK) and the
// evaluation that adds it as an opponent recognise it whatever its file is
// called.
const (
	BareModelAgent = "bare-model"
	BareModelEnv   = "BENCH_NO_TOOLS"
)

// IsBareModel reports whether a is a bare-model baseline: an agent started
// beside the workspace whose env sets BareModelEnv to "1". It is pure.
func IsBareModel(a *schemas.AgentConfig) bool {
	return a != nil && a.Adapter == "command" && a.Env[BareModelEnv] == "1"
}
