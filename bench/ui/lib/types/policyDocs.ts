// Policy document to compliance benchmark (lane Z23, handlers/policydocs.go, policybench).

export interface PolicyClause {
	number: string;
	title?: string;
	section?: string;
	text: string;
	skip?: boolean;
}

export type PolicyDraftKind = "normal" | "edge" | "adversarial" | string;
export type PolicyDraftStatus = "pending" | "accepted" | "rejected" | string;

export interface PolicyDraft {
	task: string;
	clause: string;
	kind: PolicyDraftKind;
	title: string;
	opening: string;
	check: string;
	violation: string;
	warnings?: string[];
	status: PolicyDraftStatus;
	model: string;
	cost_usd: number;
	at: string;
}

export interface PolicyDoc {
	id: string;
	name: string;
	title: string;
	format: string;
	uploaded_at: string;
	clauses: PolicyClause[];
	not_testable?: { clause: string; reason: string }[];
	drafts: PolicyDraft[];
}

export interface PolicyDocView {
	suite: string;
	dry_run?: boolean;
	doc: PolicyDoc;
	counts: Record<string, number>;
	grader: string;
	judged: boolean;
	next: string;
}

/** One document in the list; a viewer's list (limited) has the id, title and upload time only. */
export interface PolicyDocSummary {
	id: string;
	name?: string;
	title: string;
	format?: string;
	uploaded_at: string;
	clauses?: number;
	drafts?: Record<string, number>;
}

export interface PolicyDocList {
	suite: string;
	docs: PolicyDocSummary[];
	limited?: boolean;
}

export interface PolicyDocUpload {
	name: string;
	content_base64?: string;
	text?: string;
	dry_run?: boolean;
}

export interface PolicyReviewRequest {
	accept?: string[];
	reject?: string[];
	switch_grader?: boolean;
	judge?: { provider: string; model: string };
}

export interface PolicyReviewResult {
	accepted: string[];
	rejected: string[];
	grader_changed: boolean;
	counts: Record<string, number>;
	next: string;
}

export interface PolicyRate {
	runs: number;
	passed: number;
	rate: number;
	lo: number;
	hi: number;
}

export interface PolicyClauseResult {
	number: string;
	title: string;
	total: PolicyRate;
	by_kind: Record<string, PolicyRate>;
}

export interface PolicyFinding {
	clause: string;
	kind: string;
	text: string;
	examples: { run_id: string; exp_id: string; task: string; reason?: string }[];
}

export interface PolicyAgentReport {
	agent: string;
	total: PolicyRate;
	by_kind: Record<string, PolicyRate>;
	clauses: PolicyClauseResult[];
	findings: PolicyFinding[];
}

export interface PolicyReport {
	suite: string;
	doc: string;
	title: string;
	clauses: number;
	tested_clauses: number;
	accepted_tasks: number;
	pending_tasks: number;
	runs: number;
	agents: PolicyAgentReport[];
	untested_clauses: string[];
	notes: string[];
}

/** Params of the policy.generate job (cmd/bifrost-bench/jobs_policy.go). */
export interface PolicyGenerateParams {
	suite: string;
	doc: string;
	provider: string;
	model: string;
	budget_usd?: number;
	estimate?: boolean;
	yes?: boolean;
}
