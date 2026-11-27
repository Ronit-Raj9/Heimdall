// Pure helpers of the run page (fix wave lane 5a), so their rules are tested without a page.
import type { Crumb } from "@/components/shell/pageHeader";
import type { RouteTab } from "@/components/shell/routeTabs";
import type { ExperimentStatus, RunDetail } from "@/lib/types/bench";

/** Tabs every run has, in their fixed order. Every other tab (only some runs have it) comes after these. */
const FIXED_TABS = ["overview", "timeline", "output", "evidence"] as const;

/**
 * The run page tabs in a fixed order (M5-13): the tabs every run has keep their place, and the tabs only
 * some runs have (replay, recording, agents, visual, voice, gpu, services, security) follow Proof in their
 * given order, so a tab never moves between two runs.
 */
export function orderRunTabs(tabs: readonly RouteTab[]): RouteTab[] {
	const fixed = FIXED_TABS.map((id) => tabs.find((tab) => tab.id === id)).filter((tab): tab is RouteTab => Boolean(tab));
	const rest = tabs.filter((tab) => !(FIXED_TABS as readonly string[]).includes(tab.id));
	return [...fixed, ...rest];
}

/** A gate's plain name (uf5a gates.names.<id>); an unknown gate keeps its id. */
export function gateName(id: string, t: (key: string, opts: { defaultValue: string }) => string): string {
	return t(`gates.names.${id}`, { defaultValue: id });
}

/**
 * The search of "Run this task again" (M5-5): the evaluate wizard with this run's agent, model, benchmark
 * and task, opened at the cost step. Nothing starts from the link.
 */
export function rerunSearch(d: Pick<RunDetail, "slot" | "run">, setups?: ExperimentStatus["setups"]): Record<string, string | number> {
	const setup = setups?.find((s) => s.arm === d.slot.arm);
	const out: Record<string, string | number> = { suite: d.slot.suite, task: d.slot.task, step: 4 };
	const agent = setup?.agent || d.run.agent.config_id;
	if (agent) out.agent = agent;
	if (setup?.model) out.model = setup.model;
	return out;
}

export interface RunCrumbInput {
	task: string;
	expId: string;
	expName?: string;
	evaluationId?: string;
	evaluationName?: string;
	labels: { runs: string; evaluations: string; run: string };
	/** The runs list filters the user came from (M5-7); the Runs crumb keeps them. */
	runsSearch?: Record<string, string>;
}

/**
 * The run page trail (M5-7, M1-7): a run of an evaluation reads "Evaluations > <evaluation> > <comparison>
 * > Run <task>", any other "Runs > <comparison> > Run <task>", the Runs crumb back to the filtered list. Names, never raw ids, once loaded.
 */
export function runCrumbs(input: RunCrumbInput): Crumb[] {
	const comparison: Crumb = { label: input.expName || input.expId, to: "/experiments/$expId", params: { expId: input.expId } };
	const run: Crumb = { label: input.labels.run };
	if (input.evaluationId) {
		// M1-7: from=eval keeps Evaluations lit in the sidebar on the comparison page.
		comparison.search = { from: "eval" };
		return [
			{ label: input.labels.evaluations, to: "/evaluations" },
			{ label: input.evaluationName || input.evaluationId, to: "/evaluations/$evalId", params: { evalId: input.evaluationId } },
			comparison,
			run,
		];
	}
	const runs: Crumb = { label: input.labels.runs, to: "/runs" };
	if (input.runsSearch) runs.search = input.runsSearch;
	return [runs, comparison, run];
}

/**
 * The grade the "What the agent said" card shows (R3-2). When the cause card blames a tool server, the
 * grader's own reason (often "test run broken by the patch") is kept but explained as a result of that
 * error, so the page tells one story. Otherwise the grade is unchanged.
 */
export function gradeAfterCause<G extends { reason?: string | null }>(
	grade: G | null | undefined,
	toolCause: boolean,
	explain: (reason: string) => string,
): G | undefined {
	if (!grade) return undefined;
	const reason = grade.reason?.trim().replace(/[.]+$/, "");
	if (!toolCause || !reason) return grade;
	return { ...grade, reason: explain(reason) };
}
