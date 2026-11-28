import "@/components/compare/ufk2I18n";

import { LoadError } from "@/components/runs/screenStates";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import type { RunListRow } from "@/lib/types/bench";
import { useTranslation } from "react-i18next";

/** How often the tries are read again while the evaluation runs. */
const POLL_MS = 15_000;

export interface TaskTries {
	task: string;
	arm: string;
	solved: number;
	done: number;
	/** Tries not finished yet: trials minus done, never below 0. */
	left: number;
}

export interface Reliability {
	tasks: TaskTries[];
	/** Counted over tasks with at least one finished try. */
	always: number;
	some: number;
	never: number;
}

/**
 * Per task "solved k of n" from the runs already sealed (the same counted, clean runs the report's
 * pass^k uses: reportcard reliabilityCards). Runs under a fault, retried attempts that do not count,
 * and runs still going are left out; a task with no sealed try yet shows only its tries to go.
 */
export function taskReliability(rows: readonly RunListRow[], trials: number): Reliability {
	const by = new Map<string, TaskTries>();
	for (const r of rows) {
		if (r.condition && r.condition !== "clean") continue;
		const key = `${r.arm}\u0000${r.task}`;
		let e = by.get(key);
		if (!e) {
			e = { task: r.task, arm: r.arm, solved: 0, done: 0, left: 0 };
			by.set(key, e);
		}
		if (!r.sealed || !r.counted) continue;
		e.done++;
		if (r.outcome === "pass") e.solved++;
	}
	const tasks = [...by.values()].sort((x, y) => x.arm.localeCompare(y.arm) || x.task.localeCompare(y.task));
	let always = 0;
	let some = 0;
	let never = 0;
	for (const e of tasks) {
		e.left = Math.max(0, trials - e.done);
		if (e.done === 0) continue;
		if (e.solved === e.done) always++;
		else if (e.solved === 0) never++;
		else some++;
	}
	return { tasks, always, some, never };
}

/** One benchmark's reliability on the evaluation page: a headline and "solved 2 of 3" per task, live while tries run. */
export function TaskReliability({
	expId,
	trials,
	active,
	multiArm,
}: {
	expId: string;
	trials: number;
	active: boolean;
	multiArm: boolean;
}) {
	const { t } = useTranslation("ufk2");
	const q = useListRunsQuery({ exp: expId, limit: 500 }, { pollingInterval: active ? POLL_MS : 0, skipPollingIfUnfocused: true });
	if (q.error)
		return <LoadError title={t("rel.error")} error={q.error} onRetry={() => void q.refetch()} testId={`reliability-error-${expId}`} />;
	if (!q.data) return null;
	return (
		<TaskReliabilityView rel={taskReliability(q.data.runs, trials)} active={active} multiArm={multiArm} testId={`reliability-${expId}`} />
	);
}

export function TaskReliabilityView({
	rel,
	active,
	multiArm,
	testId,
}: {
	rel: Reliability;
	active: boolean;
	multiArm: boolean;
	testId: string;
}) {
	const { t } = useTranslation("ufk2");
	const counted = rel.always + rel.some + rel.never;
	return (
		<div className="flex min-w-0 flex-col gap-2" data-testid={testId}>
			<p className="text-sm font-medium" data-testid={`${testId}-headline`}>
				{counted === 0
					? t("rel.waiting")
					: t(active ? "rel.headlineLive" : "rel.headline", { always: rel.always, tasks: counted, some: rel.some, never: rel.never })}
			</p>
			{rel.tasks.length > 0 && (
				<ul className="grid min-w-0 gap-x-6 gap-y-1 text-sm sm:grid-cols-2 xl:grid-cols-3">
					{rel.tasks.map((e) => (
						<li key={`${e.arm}-${e.task}`} className="flex min-w-0 items-baseline justify-between gap-3" data-testid={`${testId}-task`}>
							<span className="truncate font-mono text-xs" title={e.task}>
								{multiArm ? `${e.arm} · ${e.task}` : e.task}
							</span>
							<span
								className={
									e.done > 0 && e.solved === e.done
										? "text-chart-success-ink shrink-0 tabular-nums"
										: e.done > 0 && e.solved === 0
											? "text-chart-error-ink shrink-0 tabular-nums"
											: "text-muted-foreground shrink-0 tabular-nums"
								}
							>
								{e.done === 0
									? t("rel.taskNone", { left: e.left })
									: e.left > 0
										? t("rel.taskPending", { solved: e.solved, done: e.done, left: e.left })
										: t("rel.task", { solved: e.solved, done: e.done })}
							</span>
						</li>
					))}
				</ul>
			)}
		</div>
	);
}