// Add benchmark (lane Z20): the /onboard API (handlers/onboard.go, bench/onboard).

export interface OnboardInput {
	url?: string;
	upload?: string;
	data?: string;
	name?: string;
	connection?: string;
	hint?: string;
}

export interface OnboardMatch {
	kind: string;
	source: string;
	label: string;
	score: number;
	can_draft: boolean;
	note?: string;
}

export interface OnboardDetectResult {
	matches: OnboardMatch[];
	name?: string;
	size?: number;
	next: string;
}

export interface OnboardJudge {
	provider: string;
	model: string;
	prompt_version?: string;
	votes?: number;
}

export interface OnboardPlan {
	source: string;
	suite: string;
	title?: string;
	grader?: string;
	judge?: OnboardJudge;
	match?: string;
	mapping?: Record<string, string>;
	format?: string;
	importer?: string;
	settings?: Record<string, string>;
	limit?: number;
	split?: string;
	held_out: { pct: number; seed: number };
}

export interface OnboardSample {
	ref: string;
	fields: Record<string, string>;
}

export interface OnboardInventory {
	kind: string;
	source: string;
	title: string;
	license: string;
	unit: string;
	units: number;
	columns?: string[];
	samples: OnboardSample[];
	suggest: OnboardPlan;
	needs?: string[];
	warnings?: string[];
	estimate: { tasks: number; readiness_runs: number; judge_calls: number; assist_usd: number };
	can_draft: boolean;
	next: string;
	importers?: string[];
}

export interface OnboardCheck {
	name: string;
	want: string;
	got: string;
	passed: boolean;
	detail?: string;
}

export interface OnboardDraftTask {
	id: string;
	origin: { source: string; ref: string };
	title: string;
	instruction: string;
	checks: string[];
	controls: { kind: string; path: string; origin: string }[];
	generated?: { field: string; model: string }[];
	gates: { gate: string; ok: boolean; blocking: boolean; message?: string }[];
	held_out: boolean;
	review: "accepted" | "rejected" | "pending";
	readiness?: { admitted: boolean; checks: OnboardCheck[] };
	catch_rate?: { caught: number; total: number };
	reason?: string;
}

export interface OnboardReadiness {
	job_id?: string;
	status: "queued" | "running" | "done" | "failed";
	where: string;
	done: number;
	total: number;
	admitted: number;
	refused: number;
	error?: string;
	problems?: { ref: string; message: string }[];
	exp_id?: string;
}

export interface OnboardPublished {
	suite: string;
	tasks: string[];
	created: boolean;
	held_out: number;
	canary?: string;
	carried?: string[];
	recheck?: string[];
	job_id?: string;
}

export interface OnboardDraftSet {
	id: string;
	suite: string;
	new_suite: boolean;
	title: string;
	kind: string;
	source: string;
	input: OnboardInput;
	plan: OnboardPlan;
	grader: string;
	public: boolean;
	license?: string;
	tasks: OnboardDraftTask[];
	problems: { ref: string; message: string }[];
	notes?: string[];
	readiness?: OnboardReadiness;
	published?: OnboardPublished;
}

export interface OnboardDraftSummary {
	id: string;
	suite: string;
	title: string;
	kind: string;
	tasks: number;
	ready: number;
	readiness?: string;
	published: boolean;
	updated: string;
	input: string;
}

export interface OnboardTaskEdit {
	review?: "accepted" | "rejected" | "pending";
	held_out?: boolean;
	drop?: boolean;
}

declare module "@/lib/i18n/resources" {
	interface LaneResources {
		onboard: typeof import("@/locales/en/onboard.json");
	}
}
