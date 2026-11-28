import { useTranslation } from "react-i18next";
import "@/components/estimate/ufh2I18n";
import type { GwNeed } from "@/lib/types/gateway";

/**
 * What a feature test's result needs against the size on the form (lane H2). It mirrors
 * Need.Check in bench/gwfeature/need.go, which also refuses on the server: a size that can never
 * reach a runs or tasks need is impossible; too few expected model calls is only unlikely, since an
 * agent may make more calls than measured. Pure.
 */
export type NeedState =
	| { status: "none" }
	| {
			status: "ok" | "unlikely" | "impossible";
			line: { key: "need.runs" | "need.runsOneTrial" | "need.calls"; o: Record<string, number> };
			perRun?: { key: "need.callsHistory" | "need.callsDefault"; o: Record<string, number> };
			problem?: { key: "need.tooFewTasks" | "need.tooFewRuns" | "need.fewCalls" | "need.fewPoison"; o: Record<string, number> };
	  };

export function needState(need: GwNeed | undefined, tasksIn: number, trialsIn: number, poison: boolean): NeedState {
	if (!need || (!need.runs_per_setup && !need.model_calls)) return { status: "none" };
	let tasks = tasksIn;
	let trials = need.fixed_trials || (trialsIn > 0 ? trialsIn : 1);
	if (need.max_tasks && tasks > need.max_tasks) tasks = need.max_tasks;
	if (need.model_calls) {
		const perRun = need.calls_per_run ?? 0;
		const expected = Math.round(tasks * perRun);
		const s: NeedState = {
			status: "ok",
			line: { key: "need.calls", o: { calls: need.model_calls, tasks: need.default_tasks } },
			perRun: {
				key: need.calls_from === "history" ? "need.callsHistory" : "need.callsDefault",
				o: { perRun: Math.round(perRun * 10) / 10 },
			},
		};
		if (expected < need.model_calls)
			return {
				...s,
				status: "unlikely",
				problem: { key: "need.fewCalls", o: { tasks, expected, need: need.model_calls, pick: need.default_tasks } },
			};
		return s;
	}
	trials = Math.max(1, trials);
	const runs = need.runs_per_setup ?? 0;
	const line: { key: "need.runs" | "need.runsOneTrial"; o: Record<string, number> } =
		need.default_trials === 1
			? { key: "need.runsOneTrial" as const, o: { runs, tasks: need.default_tasks } }
			: { key: "need.runs" as const, o: { runs, tasks: need.default_tasks, trials: need.default_trials } };
	if (need.paired_tasks && tasks < need.paired_tasks)
		return { status: "impossible", line, problem: { key: "need.tooFewTasks", o: { tasks, need: need.paired_tasks } } };
	if (tasks * trials < runs)
		return { status: "impossible", line, problem: { key: "need.tooFewRuns", o: { tasks, trials, runs: tasks * trials, need: runs } } };
	if (poison && need.poison_runs && tasks < need.poison_runs)
		return { status: "unlikely", line, problem: { key: "need.fewPoison", o: { need: need.poison_runs, tasks } } };
	return { status: "ok", line };
}

/** The need line under the size fields, and why the chosen size cannot or may not reach it. */
export function NeedView({ state }: { state: NeedState }) {
	const { t: h } = useTranslation("ufh2");
	if (state.status === "none") return null;
	return (
		<div className="flex flex-col gap-1" data-testid="feature-test-need">
			<p>
				{h(state.line.key, state.line.o)}
				{state.perRun && ` ${h(state.perRun.key, state.perRun.o)}`}
			</p>
			{state.problem && (
				<p
					className={state.status === "impossible" ? "text-destructive" : "text-chart-warning-ink"}
					role={state.status === "impossible" ? "alert" : undefined}
					data-testid={`feature-test-need-${state.status}`}
				>
					{h(state.problem.key, state.problem.o)}
				</p>
			)}
		</div>
	);
}