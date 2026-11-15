package main

// The importers register themselves from init(); this file only links them.
import (
	_ "github.com/maximhq/bifrost/bench/importers/agentdojo"
	_ "github.com/maximhq/bifrost/bench/importers/harbor"
	_ "github.com/maximhq/bifrost/bench/importers/impossiblebench"
	_ "github.com/maximhq/bifrost/bench/importers/locomo"
	_ "github.com/maximhq/bifrost/bench/importers/longmemeval"
	_ "github.com/maximhq/bifrost/bench/importers/mcpmark"
	_ "github.com/maximhq/bifrost/bench/importers/swebench"
	_ "github.com/maximhq/bifrost/bench/importers/tau2"
)
