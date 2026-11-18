import type { AgentSummary } from "@/lib/types/bench";

/** How a config's image is made: built once per suite, built once, or pulled as is. */
export type BuildKind = "perSuite" | "oneImage" | "pulled";

/** One config in the frameworks matrix. */
export interface FrameworkRow {
	id: string;
	adapter: string;
	/** The harness's own stream is parsed (native evidence). */
	nativeTrace: boolean;
	parser: string;
	/** The agent reports its own steps (generic_jsonl and the like). */
	selfReported: boolean;
	provider: string;
	fallbacks: number;
	build: BuildKind;
	/** The scripted kind of a control config; empty for a real agent. */
	scripted: string;
	sealedRuns: number | null;
}

/** The build kind of a config, as the agent card words it. */
export function buildKind(agent: Pick<AgentSummary, "per_suite_build" | "dockerfile">): BuildKind {
	if (agent.per_suite_build) return "perSuite";
	return agent.dockerfile ? "oneImage" : "pulled";
}

/**
 * The matrix rows: every config that loaded, real agents first then scripted controls, each
 * group by id. A config that failed to load has nothing to compare and is left out.
 */
export function frameworkRows(agents: readonly AgentSummary[]): FrameworkRow[] {
	return agents
		.filter((agent) => !agent.error && agent.id)
		.map((agent) => ({
			id: agent.id,
			adapter: agent.adapter,
			nativeTrace: Boolean(agent.parser),
			parser: agent.parser,
			selfReported: agent.reports_events === "native",
			provider: agent.gateway.provider,
			fallbacks: agent.gateway.fallbacks?.length ?? 0,
			build: buildKind(agent),
			scripted: agent.scripted_kind,
			sealedRuns: agent.sealed_runs,
		}))
		.sort((a, b) => Number(Boolean(a.scripted)) - Number(Boolean(b.scripted)) || a.id.localeCompare(b.id));
}