/**
 * Human labels for every job kind the server registers (cmd/bifrost-bench: servejobs.go
 * registerOps plus every registerJobKind / cliJob). One map for the jobs drawer, /jobs and the
 * job page; a kind missing here shows as written. Locale catalogs (jobs.kinds, platform
 * jobs.kinds) still win where they name a kind.
 */
export const JOB_KIND_LABELS: Readonly<Record<string, string>> = {
	"agent.build": "Build agent",
	"agent.check": "Agent check",
	"agent.diff": "Compare agent versions",
	"awareness.run": "Test-awareness check",
	"blame.run": "Find why runs failed",
	"blame.replay": "Replay to find why runs failed",
	"calibrate.run": "Grader accuracy check",
	"canary.baseline": "Drift monitor baseline",
	"canary.run": "Drift monitor check",
	"catalog.import": "Add from catalog",
	"check.run": "Result check",
	"clusters.rebuild": "Group similar failures",
	"codescan.run": "Code scan",
	"evidence.archive": "Archive saved results",
	"evidence.backup": "Download a copy of your saved results",
	"evidence.mirror": "Copy saved results to storage",
	"evidence.restore": "Restore saved results",
	"evidence.verify": "Check saved results are intact",
	"experiment.extend": "Add more runs to a comparison",
	"experiment.migrate": "Update comparison format",
	"experiment.compare-refs": "Compare agent versions",
	"experiment.regate": "Re-score",
	"experiment.run": "Comparison run",
	"export.run": "Download results",
	"gpu.check": "GPU check",
	"import.run": "Import benchmark",
	"inbox.pull": "Fetch new failures",
	"inbox.source": "Add a failure source",
	"index.rebuild": "Refresh search index",
	"integrations.export": "Download results for other tools",
	"issuebench.build": "Build a test environment from issues",
	"k8s.status": "Kubernetes status",
	"livescore.compare": "Compare live quality scores",
	"livescore.init": "Set up live quality scoring",
	"livescore.pull": "Fetch live quality scores",
	"monitor.certify": "Monitor accuracy check",
	"operate.doctor": "Server health check",
	"org.import": "Import runs",
	"probe.run": "Stress test",
	"release.compliance": "Compliance pack",
	"release.verify": "Check release record",
	"scan.mcp": "MCP server scan",
	"scan.skill": "Skill scan",
	"schedule.fire": "Scheduled run",
	"simulator.spread": "Simulated-user comparison",
	"suite.admit": "Readiness check",
	"suite.lint": "Benchmark check",
	"suite.lock": "Freeze benchmark",
	"suite.quarantine-flaky": "Set aside unreliable tasks",
	"task.from-commit": "Task from commit",
	"tasks.mine": "Make tasks from past commits",
	"visual.capture": "Take screenshot",
	"visual.compare": "Compare screenshots",
	"voice.check": "Voice check",
};

/** A job kind in words: the map above, else the kind as written. */
export function jobKindLabel(kind: string): string {
	return Object.prototype.hasOwnProperty.call(JOB_KIND_LABELS, kind) ? JOB_KIND_LABELS[kind] : kind;
}
