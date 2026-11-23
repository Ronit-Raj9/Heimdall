import { agentReportCardRequest, evaluationsApi, unwrapAgentReportCard, type AgentReportCardParams, type Report } from "./evaluationsApi";

/** One choice of the agent card's evaluation picker (fix wave 3, N206). */
export interface AgentCardEvaluation {
	id: string;
	name?: string;
	created_at: string;
}

/**
 * GET /agents/{id}/report-card as the Report card tab reads it: the report of one evaluation, the models the
 * agent's evaluations used, and which evaluation that is with the other done evaluations that match.
 */
export type AgentCard = Report & {
	models?: string[];
	evaluation_id?: string;
	evaluation_name?: string;
	evaluation_created_at?: string;
	matching_evaluations?: number;
	evaluations?: AgentCardEvaluation[];
};

export interface AgentCardArgs extends AgentReportCardParams {
	/** The evaluation to show (?evaluation=); the server falls back to the newest matching one. */
	evaluation?: string;
}

/** The agent card request: version, model and evaluation only when set. */
export function agentCardRequest({ evaluation, ...rest }: AgentCardArgs): { url: string; params?: Record<string, string> } {
	const req = agentReportCardRequest(rest);
	return evaluation ? { url: req.url, params: { ...req.params, evaluation } } : req;
}

/** The wrapper answer as an AgentCard: the report (empty when none) plus the evaluation facts. */
export function unwrapAgentCard(body: unknown): AgentCard {
	const out: AgentCard = unwrapAgentReportCard(body);
	if (body && typeof body === "object" && "report" in body) {
		const b = body as Partial<AgentCard>;
		if (b.evaluation_id) out.evaluation_id = b.evaluation_id;
		if (b.evaluation_name) out.evaluation_name = b.evaluation_name;
		if (b.evaluation_created_at) out.evaluation_created_at = b.evaluation_created_at;
		if (typeof b.matching_evaluations === "number") out.matching_evaluations = b.matching_evaluations;
		if (Array.isArray(b.evaluations)) out.evaluations = b.evaluations;
	}
	return out;
}

export const agentCardApi = evaluationsApi.injectEndpoints({
	endpoints: (builder) => ({
		getAgentCard: builder.query<AgentCard, AgentCardArgs>({
			query: (args) => agentCardRequest(args),
			transformResponse: (body: unknown) => unwrapAgentCard(body),
			providesTags: (_r, _e, { agent }) => [{ type: "Evaluations", id: `card/${agent}` }],
		}),
	}),
});

export const { useGetAgentCardQuery } = agentCardApi;