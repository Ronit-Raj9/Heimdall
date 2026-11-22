import type { JsonValue, Verdict, WithMode } from "@/lib/types/bench";

/** The analysis job kinds (web parity lane L15, cmd/bifrost-bench/jobs_analysis.go). */
export type AnalysisJobKind = "check.run" | "codescan.run" | "awareness.run" | "blame.run" | "clusters.rebuild" | "index.rebuild";

/** Which views of the analysis tab a finished job refreshes. */
export type AnalysisView = "verdict" | "awareness" | "clusters";

/** check.run's result. */
export interface CheckRunResult {
	exp_id: string;
	analysis_id: string;
	grade_source: string;
	decision: string;
	action: string;
	/** The release rule set's decision, when one applies (never on a one-setup score run). */
	policy?: string;
	/** The rule set's file name without .yaml, as Releases > Rules lists it. */
	policy_file?: string;
}

/** codescan.run's result. */
export interface CodeScanRunResult {
	exp_id: string;
	runs: number;
	scanned: number;
	skipped: number;
	findings: number;
	by_severity: Record<string, number>;
	vetoed: string[];
}

/** blame.run's result. */
export interface BlameRunResult {
	exp_id: string;
	runs: number;
	failed: number;
	/** Runs that were not scored (infrastructure or grader problem), not counted in failed. */
	not_scored?: number;
	blamed: number;
	skipped: number;
}

/** clusters.rebuild's result. */
export interface ClustersRebuildResult {
	runs: number;
	failures: number;
	clusters: number;
	experiments: number;
}

/** POST /audit's answer: the verdict for a published per-task table. */
export type AuditVerdictResponse = WithMode<Verdict>;

/** A job result as the jobs API returns it. */
export type JobResult = Record<string, JsonValue> | undefined;

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		analysis: typeof import("@/locales/en/analysis.json");
	}
}