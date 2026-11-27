import { isTestObject } from "@/lib/testObjects";

/** One evaluation an agent card can show (GET /agents/{id}/report-card `evaluations`, newest first). */
export interface AgentEvaluationChoice {
	id: string;
	name?: string;
}

/** True when the entry reads as a failure test (its saved name starts so; the list has no mode yet). */
export function looksLikeFailureTest(ev: { name?: string }): boolean {
	return /^failure test/i.test(ev.name ?? "");
}

/** What the pick reads of GET /agents/{id}/report-card. */
export interface AgentCardPickFields {
	evaluation_id?: string;
	evaluation_name?: string;
	evaluations?: readonly AgentEvaluationChoice[];
	reliability?: readonly { fault: string }[];
}

/**
 * The evaluation an agent's report card should switch to when nothing names one (Q-17, M3-2), or
 * undefined to keep the server's pick (the newest). The server's pick is kept unless it is a test
 * object ("zz-test-..." names) or a failure test; then the newest plain evaluation that is not a
 * test object, else (for a test object) any other one that is not. Home's agent cards and the agent
 * page's Report card tab both use it, so both show the same evaluation. Pure.
 */
export function preferredAgentEvaluation(card: AgentCardPickFields): string | undefined {
	const shownId = card.evaluation_id;
	if (!shownId) return undefined;
	const list = card.evaluations ?? [];
	const shown = list.find((e) => e.id === shownId);
	const shownName = card.evaluation_name || shown?.name || shownId;
	const shownIsTest = isTestObject(shownName) || isTestObject(shownId);
	const shownIsFault = looksLikeFailureTest({ name: shownName }) || (card.reliability ?? []).some((r) => r.fault !== "clean");
	if (!shownIsTest && !shownIsFault) return undefined;
	const others = list.filter((e) => e.id !== shownId && !isTestObject(e.name || e.id) && !isTestObject(e.id));
	return (others.find((e) => !looksLikeFailureTest(e)) ?? (shownIsTest ? others[0] : undefined))?.id;
}
