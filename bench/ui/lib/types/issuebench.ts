// Issues to benchmark (lane G06): mirrors bench/issuebench types and pipeline.State.
export interface IssueCandidate {
	repo: string;
	pr: number;
	issue: number;
	issue_title: string;
	issue_body: string;
	pr_merged: string;
	test_files: string[] | null;
	code_files: string[] | null;
	skip?: string;
}

export interface IssueFunnel {
	merged_prs: number;
	linked: number;
	qualified: number;
	reasons: Record<string, number> | null;
}

export type IssueStatus = "queued" | "building" | "built" | "validated" | "ready" | "failed" | "accepted" | "rejected";
export type IssueFix = "add_secret" | "allow_service" | "raise_budget" | "retry" | "open_workers";

/** The pipeline stage a worker job runs for an issue (issuebench.Stage*). */
export type IssueStage = "build" | "validate" | "checks";

export interface IssueValidation {
	fail_to_pass: string[] | null;
	pass_to_pass: string[] | null;
}

export interface IssueItem {
	candidate: IssueCandidate;
	version: string;
	status: IssueStatus;
	reason?: string;
	fix?: IssueFix | "";
	validation?: IssueValidation;
	instruction?: string;
	/** The job running or last run for this issue (hosted site). */
	job_id?: string;
	stage?: IssueStage;
}

export interface IssueLogLine {
	seq: number;
	at: string;
	line: string;
}

export interface IssueRun {
	id: string;
	repo: string;
	status: "scanned" | "running" | "done" | "failed" | "canceled";
	consent: boolean;
	builder_model?: { provider?: string; model?: string; key_id?: string };
	detail?: string;
	funnel: IssueFunnel;
	items: IssueItem[] | null;
	benchmark?: string;
	cost_usd: number;
	created: string;
	log?: IssueLogLine[] | null;
}

export interface IssueRetryBody {
	fix: IssueFix;
	secrets?: string[];
	allow_services?: string[];
	budget_usd?: number;
}

/** GET /issuebench/readiness (issuebench.Readiness). */
export interface IssueReadiness {
	hosted: boolean;
	/** A worker takes issuebench.work now; always true locally. */
	worker_online: boolean;
	/** A GitHub token for the scan. */
	github: boolean;
	/** The server has a model gateway. */
	gateway: boolean;
	/** BENCH_ISSUEBENCH_PROVIDER and _MODEL are set. */
	default_model: boolean;
	message?: string;
}