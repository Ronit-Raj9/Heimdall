// Package marble imports MultiAgentBench (the MARBLE repository,
// github.com/ulab-uiuc/MARBLE) tasks as team tasks: each upstream agent
// becomes a member of the task's team (task.yaml team:), the relationship
// graph becomes the routes members may message each other on through the
// team mailbox, and the scoring becomes a grader. The database scenario is
// verified: PostgreSQL and a database tool run as services, the anomaly is
// replayed before the agents start, and a hidden verifier checks the named
// root causes against the task's labels with no model. Research and coding
// are scored only by an LLM judge upstream, so they import into a judged
// suite. Minecraft and bargaining are refused with the reason. See
// docs/importers-marble.md.
package marble
