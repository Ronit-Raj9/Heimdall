package main

// The importers register themselves from init(); this file only links them.
import (
	_ "github.com/maximhq/bifrost/bench/importers/agentdojo"
	_ "github.com/maximhq/bifrost/bench/importers/agentvista"      // L05: AgentVista multimodal agent questions
	_ "github.com/maximhq/bifrost/bench/importers/bigfinancebench" // L07: BigFinanceBench public finance questions
	_ "github.com/maximhq/bifrost/bench/importers/browsecompplus"  // X5a
	_ "github.com/maximhq/bifrost/bench/importers/cheatbench"      // L06: CheatBench cheating environments
	_ "github.com/maximhq/bifrost/bench/importers/clbench"         // L12: Continual Learning Bench
	_ "github.com/maximhq/bifrost/bench/importers/cooperbench"     // L11: CooperBench two-agent pairs
	_ "github.com/maximhq/bifrost/bench/importers/enterpriseops"   // L14: EnterpriseOps-Gym
	_ "github.com/maximhq/bifrost/bench/importers/gaia"            // X5a
	_ "github.com/maximhq/bifrost/bench/importers/harbor"
	_ "github.com/maximhq/bifrost/bench/importers/impossiblebench"
	_ "github.com/maximhq/bifrost/bench/importers/kernelbench" // X7: KernelBench level 1, GPU tasks
	_ "github.com/maximhq/bifrost/bench/importers/locomo"
	_ "github.com/maximhq/bifrost/bench/importers/longmemeval"
	_ "github.com/maximhq/bifrost/bench/importers/marble" // X5b: MultiAgentBench (MARBLE)
	_ "github.com/maximhq/bifrost/bench/importers/mcpmark"
	_ "github.com/maximhq/bifrost/bench/importers/msibench"     // MSI-Bench v2 multi-speaker room calls
	_ "github.com/maximhq/bifrost/bench/importers/programbench" // L08: ProgramBench, whole-program rebuilds
	_ "github.com/maximhq/bifrost/bench/importers/skillpool"    // L14: Skill-Usage 34k pool
	_ "github.com/maximhq/bifrost/bench/importers/swebench"
	_ "github.com/maximhq/bifrost/bench/importers/tau2"
	_ "github.com/maximhq/bifrost/bench/importers/toolbenchx"   // L10: ToolBench-X
	_ "github.com/maximhq/bifrost/bench/importers/vendingbench" // L12: Prosus Vending Bench
	_ "github.com/maximhq/bifrost/bench/importers/voicebench"   // B5: VoiceBench spoken questions
	_ "github.com/maximhq/bifrost/bench/importers/webarenapro"  // L03: WebArena-Pro (Harbor export)
)
