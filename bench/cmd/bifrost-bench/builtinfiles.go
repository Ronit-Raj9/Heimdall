package main

import (
	"github.com/maximhq/bifrost/bench/examples/frameworks"
	"github.com/maximhq/bifrost/bench/utils"
)

// The framework agents' build folders ship in the binary: the server and the
// worker (both this command) resolve an agent's build files and unedited
// older copies against them (utils.BuiltinAgentsRoot).
func init() { utils.RegisterBuiltinFiles(frameworks.Files, "examples/frameworks") }
