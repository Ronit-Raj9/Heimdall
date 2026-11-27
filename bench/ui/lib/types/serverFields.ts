// Optional fields the server sends since the fix wave (fixwave/notes/SERVER.md). The server is not
// deployed with all of them yet and may never send some: every field is optional, and a screen keeps
// its old behaviour when one is missing. Shared here so page lanes stop declaring local copies.
import type { ExperimentStatus } from "@/lib/types/bench";
import type { RetentionPolicy } from "@/lib/types/dataops";

/** GET /runs rows and GET /runs/{id}: readable names next to the ids. */
export interface RunTitleFields {
	task_title?: string;
	suite_title?: string;
	/** Runs list only: the comparison's name. */
	exp_name?: string;
	/** A short reason the run failed, when the server has one. */
	fail_reason?: string;
}

/** GET /runs/{id}?failure=1: the failure group of a run that did not pass. */
export interface RunFailureGroup {
	signature?: string;
	title?: string;
	text?: string;
	/** Runs of the same evaluation with the same signature, this run included. */
	count?: number;
	exp_id?: string;
}

export interface RunFailureFields {
	failure?: RunFailureGroup;
}

/** One setup's raw numbers from GET /experiments?arm_stats=1. */
export interface ArmStat {
	arm: string;
	runs: number;
	passed: number;
	/** Null with no run. */
	pass_rate: number | null;
	/** Null unless every counted run has an observed cost (an unpriced run is never $0). */
	cost_usd: number | null;
	cost_runs: number;
}

/** A comparisons list row with the optional per-setup numbers. */
export type ExperimentRow = ExperimentStatus & { arm_stats?: ArmStat[] };

/** The paging fields of a paged list answer (G3). */
export interface PageFields {
	total?: number;
	limit?: number;
	offset?: number;
}

/** GET /data/retention?preview_days=N: what a retention of `days` would delete (an upper bound). */
export interface RetentionPreview {
	days: number;
	experiments: number;
	runs: number;
}

export type RetentionWithPreview = RetentionPolicy & { preview?: RetentionPreview };

/** GET /evaluations/{id}: a skipped child benchmark says why with a fixed code (for example "no_worker"). */
export interface ChildCauseFields {
	cause?: string;
}

/** POST /evaluations/estimate: where the cost came from ("list_price" for an arm with no history). */
export interface EstimateBasisFields {
	estimate_basis?: string;
}

/** GET /evaluations/{id}/release-preview: a fixed code beside the problem sentence. */
export interface ProblemCodeFields {
	problem_code?: string;
}

/** GET /shares rows: the creator's display name. */
export interface CreatedByNameFields {
	created_by_name?: string;
}

/** GET /connect/traces: the name of the secret the server reads (HEIMDALL_INGEST_TOKEN). */
export interface TokenEnvFields {
	token_env?: string;
}

/** POST /findings: the comparison and setup the finding came from. */
export interface FindingSourceFields {
	experiment?: string;
	arm?: string;
}