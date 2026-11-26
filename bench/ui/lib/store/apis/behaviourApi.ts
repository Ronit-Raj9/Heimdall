import { benchApi } from "./benchApi";

/** One step selector of a behaviour check (steprules.Selector). */
export interface StepSelector {
	kinds?: string[];
	class?: string;
	tool?: string;
	command?: string;
	path?: string;
	op?: string;
	is_error?: boolean;
	witnessed?: boolean;
}

export type RuleKind = "present" | "absent" | "sequence" | "before" | "repeat" | "after_error";

/** One behaviour check (steprules.Rule). */
export interface BehaviourRule {
	id: string;
	title: string;
	description?: string;
	kind: RuleKind;
	a: StepSelector;
	b?: StepSelector;
	reset?: StepSelector;
	n?: number;
	window?: number;
	version: number;
}

export interface StepClasses {
	test_commands: string[];
	edit_tools: string[];
	read_tools: string[];
	read_commands: string[];
	shell_tools: string[];
	claim_words: string[];
}

export interface BehaviourChecksResponse {
	builtin: BehaviourRule[];
	rules: BehaviourRule[];
	classes: StepClasses;
}

export interface RuleHit {
	seqs: number[];
	summary: string;
	witnessed: boolean;
}

export interface RuleResult {
	rule_id: string;
	rule_version: number;
	status: "fired" | "clear" | "unknown";
	hits?: RuleHit[];
	reason?: string;
}

export interface RunBehaviourResponse {
	run_id: string;
	available: boolean;
	truncated: boolean;
	results: RuleResult[];
}

export interface BehaviourArmCounts {
	fired: number;
	runs: number;
	unknown: number;
	fired_passed: number;
	fired_failed: number;
}

export interface ExperimentBehaviourResponse {
	exp_id: string;
	rules: { rule: BehaviourRule; per_arm: Record<string, BehaviourArmCounts>; runs: Record<string, string[]> }[];
	partial?: boolean;
}

export interface SideAfter {
	steps: number;
	turns: number;
	retries: number;
	test_runs: number;
	edits: number;
	errors: number;
	cost_usd?: number;
}

export interface Divergence {
	found: boolean;
	category?: "command" | "file_edit" | "tool_call" | "tool_result" | "retry" | "test_run" | "ended";
	seq_a?: number;
	seq_b?: number;
	text_a?: string;
	text_b?: string;
	after: [SideAfter, SideAfter];
	note: string;
}

export interface PairDivergenceResponse {
	pair_id: string;
	arms: [string, string];
	runs: [string, string];
	outcomes: [string, string];
	available: boolean;
	divergence?: Divergence;
	reason?: string;
}

/** Lane I28: first divergence of two runs and behaviour checks, injected into the bench API. */
export const behaviourApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchBehaviour"] }).injectEndpoints({
	endpoints: (builder) => ({
		getPairDivergence: builder.query<PairDivergenceResponse, string>({
			query: (pairId) => `/pairs/${encodeURIComponent(pairId)}/divergence`,
		}),
		getBehaviourChecks: builder.query<BehaviourChecksResponse, void>({
			query: () => "/behaviour-checks",
			providesTags: [{ type: "BenchBehaviour", id: "CHECKS" }],
		}),
		putBehaviourChecks: builder.mutation<BehaviourChecksResponse, { rules: BehaviourRule[] }>({
			query: (body) => ({ url: "/behaviour-checks", method: "PUT", body }),
			invalidatesTags: [
				{ type: "BenchBehaviour", id: "CHECKS" },
				{ type: "BenchBehaviour", id: "RESULTS" },
			],
		}),
		getRunBehaviour: builder.query<RunBehaviourResponse, string>({
			query: (runId) => `/runs/${encodeURIComponent(runId)}/behaviour`,
			providesTags: [{ type: "BenchBehaviour", id: "RESULTS" }],
		}),
		getExperimentBehaviour: builder.query<ExperimentBehaviourResponse, string>({
			query: (expId) => `/experiments/${encodeURIComponent(expId)}/behaviour`,
			providesTags: [{ type: "BenchBehaviour", id: "RESULTS" }],
		}),
	}),
});

export const {
	useGetPairDivergenceQuery,
	useGetBehaviourChecksQuery,
	usePutBehaviourChecksMutation,
	useGetRunBehaviourQuery,
	useGetExperimentBehaviourQuery,
} = behaviourApi;