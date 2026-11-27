import type { SuiteSummary, SuiteTask } from "@/lib/types/bench";

/** First workflow: the provenance tool name `heimdall tasks mine` writes into task.yaml. */
export const MINED_BY = "tasks mine";

/** One suite's mined tasks: how many there are and which still have a tool-drafted instruction nobody reviewed. */
export interface MinedSuiteRow {
	suite: string;
	tasks: number;
	mined: number;
	admittedMined: number;
	unreviewed: SuiteTask[];
}

/** A task whose instruction a tool drafted (tasks mine) and nobody has reviewed: instruction_reviewed is false. */
export function isUnreviewed(task: SuiteTask): boolean {
	return task.provenance?.instruction_reviewed === false;
}

/** Per suite, the mined tasks and the unreviewed ones, suites with mined tasks first, then by id. */
export function minedRows(suites: readonly SuiteSummary[]): MinedSuiteRow[] {
	const rows = suites.filter((s) => !isTestSuite(s.id)).map((s) => {
		const mined = s.tasks.filter((t) => t.provenance?.created_by === MINED_BY);
		return {
			suite: s.id,
			tasks: s.tasks.length,
			mined: mined.length,
			admittedMined: mined.filter((t) => t.admitted).length,
			unreviewed: s.tasks.filter(isUnreviewed),
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
