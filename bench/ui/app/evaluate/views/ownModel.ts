import { useGetAgentsQuery } from "@/lib/store/apis/benchApi";
import type { AgentSummary } from "@/lib/types/bench";
import { useMemo } from "react";

/**
 * Agents that bring their own model (lane X10, J2): an HTTP endpoint, an A2A agent or an
 * OpenAI-compatible endpoint that names its model. An evaluation arm on one of them may leave the
 * model blank, which means "the agent's own model" and is reported as such.
 */

export const OWN_MODEL_LABEL = "its own model";

/** Config ids of the agents whose summary says owns_model. */
export function ownModelAgents(agents: readonly Pick<AgentSummary, "id" | "owns_model">[] | undefined): Set<string> {
	return new Set((agents ?? []).filter((a) => a.owns_model).map((a) => a.id));
}

/** The org's own-model agents, from GET /agents. */
export function useOwnModelAgents(): Set<string> {
	const q = useGetAgentsQuery();
	const agents = (q.data as { agents?: AgentSummary[] } | undefined)?.agents;
	return useMemo(() => ownModelAgents(agents), [agents]);
}

/** Whether an arm is complete: a model, or a blank model on an agent that brings its own. */
export function armHasModel(arm: { agent: string; model: string }, owns: ReadonlySet<string> = new Set()): boolean {
	return arm.model.trim() !== "" || owns.has(arm.agent);
}
