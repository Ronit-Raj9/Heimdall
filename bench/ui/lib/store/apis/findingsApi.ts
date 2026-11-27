import type { WithMode } from "@/lib/types/bench";
import { benchApi } from "./benchApi";

/** Finding states (bench/findings). */
export type FindingState = "open" | "expected" | "fixed" | "reopened";
export type FindingSource = "inbox" | "cluster" | "investigation" | "check";

export interface FindingTaskRef {
	suite: string;
	task: string;
}

export interface FindingQuote {
	run_id: string;
	seq: number;
	text: string;
}

export interface FindingFixProof {
	experiment: string;
	agent: string;
	fixed_hash: string;
	old_hash: string;
	fixed_passed: number;
	fixed_runs: number;
	old_passed: number;
	old_runs: number;
	at: string;
}

export interface FindingEvent {
	at: string;
	by: string;
	from: string;
	to: string;
	reason: string;
	experiment?: string;
	run_id?: string;
}

/** One finding (GET /findings/{id}, bench/findings Finding). */
export interface Finding {
	id: string;
	key: string;
	title: string;
	source: FindingSource;
	origin: "verified" | "rule" | "model";
	state: FindingState;
	tasks: FindingTaskRef[];
	runs: string[];
	hypothesis?: string;
	quotes?: FindingQuote[];
	fix?: FindingFixProof;
	reruns?: string[];
	history: FindingEvent[];
	/** The comparison and setup the failure came from, once the server sends them (M7-27); until then the history names the comparison. */
	experiment?: string;
	arm?: string;
	created_at: string;
	updated_at: string;
}

export type FindingsListResponse = WithMode<{
	findings: Finding[];
	counts: Partial<Record<FindingState, number>>;
}>;

export interface FindingRerunRequest {
	exp_id: string;
	arm: string;
	trials: number;
	yes: true;
}

export type FindingRerunResponse = WithMode<{
	rerun: {
		parent_id: string;
		experiment_id: string;
		tasks: number;
		slots: number;
		started: boolean;
		start_error?: string;
	};
	finding: Finding;
	note?: string;
}>;

/** One row of GET /investigations (bench/investigator, served by another lane). */
export interface InvestigationSummary {
	id: string;
	exp_id: string;
	created_at: string;
	runs: string[] | number;
	items: unknown[] | number;
	spent_usd: number;
	stopped?: string;
}

export interface InvestigationGroup {
	title: string;
	kind: string;
	items: string[];
	runs: number;
	tasks: string[];
}

export type InvestigationReport = WithMode<{
	id: string;
	exp_id: string;
	question: string;
	groups: InvestigationGroup[];
	spent_usd: number;
	budget_usd: number;
	stopped?: string;
	note: string;
	created_at: string;
}>;

/** Findings (handlers/findings_w443.go) and the investigation reports the Findings tab lists. */
export const findingsApi = benchApi.enhanceEndpoints({ addTagTypes: ["BenchFindings", "BenchInvestigations"] }).injectEndpoints({
	endpoints: (builder) => ({
		getFindings: builder.query<FindingsListResponse, FindingState | void>({
			query: (state) => (state ? { url: "/findings", params: { state } } : "/findings"),
			providesTags: [{ type: "BenchFindings", id: "LIST" }],
		}),
		getFinding: builder.query<WithMode<Finding>, string>({
			query: (id) => `/findings/${encodeURIComponent(id)}`,
			providesTags: (_r, _e, id) => [{ type: "BenchFindings", id }],
		}),
		addFinding: builder.mutation<WithMode<Finding>, { source: FindingSource; ref: string }>({
			query: (body) => ({ url: "/findings", method: "POST", body }),
			invalidatesTags: [{ type: "BenchFindings", id: "LIST" }],
		}),
		findingReason: builder.mutation<WithMode<Finding>, { id: string; action: "expected" | "reopen"; reason: string }>({
			query: ({ id, action, reason }) => ({
				url: `/findings/${encodeURIComponent(id)}/${action}`,
				method: "POST",
				body: { reason },
			}),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "BenchFindings", id: "LIST" },
				{ type: "BenchFindings", id },
			],
		}),
		rerunFinding: builder.mutation<FindingRerunResponse, { id: string; body: FindingRerunRequest }>({
			query: ({ id, body }) => ({
				url: `/findings/${encodeURIComponent(id)}/rerun`,
				method: "POST",
				body,
			}),
			invalidatesTags: (_r, _e, { id }) => [
				{ type: "BenchFindings", id: "LIST" },
				{ type: "BenchFindings", id },
				{ type: "BenchExperiment", id: "LIST" },
			],
		}),
		checkFinding: builder.mutation<WithMode<Finding>, string>({
			query: (id) => ({
				url: `/findings/${encodeURIComponent(id)}/check`,
				method: "POST",
				body: {},
			}),
			invalidatesTags: (_r, _e, id) => [
				{ type: "BenchFindings", id: "LIST" },
				{ type: "BenchFindings", id },
			],
		}),
		getInvestigations: builder.query<WithMode<{ investigations?: InvestigationSummary[] }> | InvestigationSummary[], void>({
			query: () => "/investigations",
			providesTags: [{ type: "BenchInvestigations", id: "LIST" }],
		}),
		getInvestigation: builder.query<InvestigationReport, string>({
			query: (id) => `/investigations/${encodeURIComponent(id)}`,
			providesTags: (_r, _e, id) => [{ type: "BenchInvestigations", id }],
		}),
	}),
});

export const {
	useGetFindingsQuery,
	useGetFindingQuery,
	useAddFindingMutation,
	useFindingReasonMutation,
	useRerunFindingMutation,
	useCheckFindingMutation,
	useGetInvestigationsQuery,
	useGetInvestigationQuery,
} = findingsApi;