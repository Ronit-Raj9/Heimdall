/**
 * Human labels for every job kind the server registers (cmd/bifrost-bench: servejobs.go
 * registerOps plus every registerJobKind / cliJob). One map for the jobs drawer, /jobs and the
 * job page; a kind missing here shows as written. Locale catalogs (jobs.kinds, platform
 * jobs.kinds) still win where they name a kind.
 */
export const JOB_KIND_LABELS: Readonly<Record<string, string>> = {
	"agent.build": "Agent image build",
	"agent.check": "Agent check",
	"agent.diff": "Agent version diff",
	"awareness.run": "Eval awareness probe",
	"blame.run": "Failure blame",
	"calibrate.run": "Judge calibration",
	"canary.baseline": "Canary baseline",
	"canary.run": "Canary run",
	"catalog.import": "Catalog import",
	"check.run": "Verdict check",
	"clusters.rebuild": "Failure clusters rebuild",
	"codescan.run": "Code scan",
	"evidence.archive": "Evidence archive",
	"evidence.backup": "Evidence backup",
	"evidence.mirror": "Evidence mirror",
	"evidence.restore": "Evidence restore",
	"evidence.verify": "Evidence verify",
	"experiment.extend": "Experiment extend",
	"experiment.migrate": "Experiment migration",
	"experiment.regate": "Regrade",
	"experiment.run": "Experiment run",
	"export.run": "Export",
	"gpu.check": "GPU check",
	"import.run": "Benchmark import",
	"inbox.pull": "Inbox pull",
	"index.rebuild": "Index rebuild",
	"integrations.export": "Integrations export",
	"k8s.status": "Kubernetes status",
	"livescore.compare": "Live score compare",
	"livescore.init": "Live score setup",
	"livescore.pull": "Live score pull",
	"monitor.certify": "Monitor certification",
	"operate.doctor": "Doctor",
	"org.import": "Run import",
	"probe.run": "Probe run",
	"release.compliance": "Compliance pack",
	"release.verify": "Release verify",
	"scan.mcp": "MCP scan",
	"scan.skill": "Skill scan",
	"schedule.fire": "Scheduled run",
	"simulator.spread": "Simulator spread",
	"suite.admit": "Suite admission",
	"suite.lint": "Suite lint",
	"suite.lock": "Suite lock",
	"suite.quarantine-flaky": "Quarantine flaky tasks",
	"task.from-commit": "Task from commit",
	"tasks.mine": "Mine tasks",
	"visual.capture": "Visual capture",
	"visual.compare": "Visual compare",
	"voice.check": "Voice check",
};

/** A job kind in words: the map above, else the kind as written. */
export function jobKindLabel(kind: string): string {
	return Object.prototype.hasOwnProperty.call(JOB_KIND_LABELS, kind) ? JOB_KIND_LABELS[kind] : kind;
}
