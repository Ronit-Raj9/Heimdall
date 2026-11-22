/** Tasks from your API (lane Z18): handlers/apptasks.go and the apptasks package. */

export type AppTasksStatus = "new" | "rules_drafted" | "rules_approved" | "generated" | "written";
export type RuleKind = "must" | "must_not" | "escalate" | "info";
export type ReviewState = "pending" | "accepted" | "rejected";

export interface AppRule {
	id: string;
	text: string;
	kind: RuleKind;
	operations: string[];
}

export interface AppOperation {
	name: string;
	method: string;
	path: string;
	kind: string;
	collection?: string;
	description?: string;
}

export interface AppAction {
	tool: string;
	args?: Record<string, unknown>;
}

export interface AppPlan {
	scenario: {
		rule_id: string;
		title: string;
		opening_message: string;
		persona_goal: string;
		expected_actions: AppAction[];
		forbidden_actions?: AppAction[];
		reply_rubric?: string;
	};
	checks: number;
	summary: string[];
	proof: { reference: string; controls: Record<string, string>; skipped?: string[] };
}

export interface AppDropped {
	rule_id: string;
	title: string;
	reason: string;
}

export interface AppWritten {
	id: string;
	title: string;
	rule_id: string;
	checks: number;
	summary: string;
	review: ReviewState;
	reviewed_by?: string;
}

export interface AppCallRecord {
	op: string;
	model: string;
	attempts: number;
	usd: number;
	error?: string;
}

export interface AppTasksDraft {
	id: string;
	suite: string;
	app: string;
	title?: string;
	status: AppTasksStatus;
	model?: { provider: string; model: string };
	rules: AppRule[];
	approved_by?: string;
	judged: boolean;
	plans: AppPlan[];
	dropped: AppDropped[];
	written: AppWritten[];
	calls: AppCallRecord[];
	spent_usd: number;
	warnings: string[];
	operations: AppOperation[];
	collections: string[];
	next: string;
}

export interface AppTasksDraftSummary {
	id: string;
	suite: string;
	app: string;
	title: string;
	status: AppTasksStatus;
	rules: number;
	tasks: number;
	written: number;
	pending: number;
	spent_usd: number;
	created_at: string;
}

export interface AppTasksCreateRequest {
	suite: string;
	app: string;
	title?: string;
	spec: string;
	spec_name?: string;
	policy: string;
	policy_name?: string;
}

export interface AppTasksWriteResult {
	suite: string;
	created: boolean;
	tasks: AppWritten[];
	next: string;
}