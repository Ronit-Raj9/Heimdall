// Package clbench imports Continual Learning Bench (CL-Bench, Snorkel AI,
// UC Berkeley Sky Lab and UW-Madison; github.com/pgasawa/continual-learning-bench,
// Apache-2.0) as a multi-session memory suite.
//
// Upstream runs a "system" through a stream of task instances that share a
// learnable structure (a poker opponent's habits, a database schema, a
// spectrum band's channel plan) and scores how much the system improves. The
// bench has no per-instance runner for that, so each stream, a task family
// run on one named schedule, becomes ONE bench task:
//
//   - a service `clenv` (role mcp) runs the upstream task object, built as
//     `clbench run <family> --schedule <id>` builds it for run index 0, behind
//     a small MCP server (image/server.py): get_brief, start_session,
//     get_query, submit. Every response goes through upstream task.step, so
//     rewards are upstream's.
//   - one agent session per schedule stage. Sessions are fresh agent
//     processes; the service, and so the stream's state, lives for the whole
//     run, and only the agent's memory (/memory and its memory paths) carries
//     what it learned from one stage to the next.
//   - after the agent exits the service's outcomes are exported (POST
//     /finalize inside the service) and image/runverify.py writes the reward:
//     the sum of instance rewards over (instances in the schedule x upstream
//     r_max), clipped to 0..1, with completion and the raw mean as dimensions.
//
// Families whose environment the bench cannot give a service (a Docker
// daemon for per-instance containers) are imported as drafts whose reference
// control refuses with the reason. See docs/importing-clbench.md.
package clbench
