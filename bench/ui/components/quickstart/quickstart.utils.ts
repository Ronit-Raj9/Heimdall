import type { SuiteSummary, SuiteTask } from "@/lib/types/bench";

/** First workflow: the provenance tool name `heimdall tasks mine` writes into task.yaml. */
export const MINED_BY = "tasks mine";

/** One suite's mined tasks: how many there are and which still have a tool-drafted instruction nobody reviewed. */
export interface MinedSuiteRow {
	suite: string;
	tasks: number;
	mined: number;
	admittedMined: number;
	unreviewed: Pick<SuiteTask, "id" | "title">[];
}

/**
 * The mined-task facts the light suites list carries (G3, M2-20): optional, since an older server
 * or a partial entry leaves them out.
 */
export interface SuiteMinedFacts {
	task_count?: number;
	mined_tasks?: number;
	mined_admitted_tasks?: number;
	unreviewed_tasks?: { id: string; title: string }[];
}

type MinedSource = Pick<SuiteSummary, "id"> & { tasks?: readonly SuiteTask[] | null } & SuiteMinedFacts;

/** True when every light row carries the mined facts, so Quickstart needs no full suites list (M2-20, M4-34). */
export function lightHasMinedFacts(suites: readonly MinedSource[]): boolean {
	return suites.every((s) => typeof s.mined_tasks === "number" && Array.isArray(s.unreviewed_tasks));
}

/** A task whose instruction a tool drafted (tasks mine) and nobody has reviewed: instruction_reviewed is false. */
export function isUnreviewed(task: SuiteTask): boolean {
	return task.provenance?.instruction_reviewed === false;
}

/** Per suite, the mined tasks and the unreviewed ones, suites with mined tasks first, then by id. */
export function minedRows(suites: readonly MinedSource[]): MinedSuiteRow[] {
	const rows = suites.filter((s) => !isTestSuite(s.id)).map((s) => {
		if (typeof s.mined_tasks === "number" && Array.isArray(s.unreviewed_tasks)) {
			return {
				suite: s.id,
				tasks: s.task_count ?? s.tasks?.length ?? 0,
				mined: s.mined_tasks,
				admittedMined: s.mined_admitted_tasks ?? 0,
				unreviewed: s.unreviewed_tasks.map((t) => ({ id: t.id, title: t.title })),
			};
		}
		const tasks = s.tasks ?? [];
		const mined = tasks.filter((t) => t.provenance?.created_by === MINED_BY);
		return {
			suite: s.id,
			tasks: tasks.length,
			mined: mined.length,
			admittedMined: mined.filter((t) => t.admitted).length,
			unreviewed: tasks.filter(isUnreviewed),
		};
	});
	return rows.sort((a, b) => Number(b.mined > 0) - Number(a.mined > 0) || a.suite.localeCompare(b.suite));
}

/** Total unreviewed tasks over every suite. */
export function unreviewedTotal(rows: readonly MinedSuiteRow[]): number {
	return rows.reduce((n, r) => n + r.unreviewed.length, 0);
}

/** The command that marks one task's instruction reviewed. */
export function reviewCommand(suite: string, task: string): string {
	return `heimdall task review ${suite}/${task}`;
}

/** Suites made by tests (`zz-test-` prefix), kept out of the mined list like test agents (M2-19). */
export function isTestSuite(id: string): boolean {
	return id.startsWith("zz-test-");
}

/** Step 0 on /quickstart: build the command line tool from this repository (M2-17). */
export const INSTALL_STEP = { key: "install", command: "cd bench && make heimdall" } as const;

/** The four commands of the first workflow, in order, with their i18n keys. */
export const QUICKSTART_STEPS = [
	{ key: "init", command: "heimdall init" },
	{ key: "mine", command: "heimdall tasks mine --limit 10" },
	{ key: "compare", command: "heimdall compare --baseline main --candidate HEAD --base agents/claude-code.yaml --trials 3 --fault model-outage" },
	{ key: "ci", command: "heimdall ci install github" },
] as const;
