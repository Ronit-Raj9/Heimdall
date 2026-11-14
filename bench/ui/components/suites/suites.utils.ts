import type {
	AdmissionControl,
	PS2Family,
	SuiteAdmissionCheck,
	SuiteLockState,
	SuiteProbe,
	SuiteSummary,
	SuiteTask,
} from "@/lib/types/bench";

// Pure helpers of the suites screen: admission state, filters, sorting, coverage and grouping.
// Everything here is testable without a browser.

/** What the admission column shows: the lock status, with "stale" when files changed since. */
export type AdmissionState = "admitted" | "stale" | "draft" | "not_locked" | "quarantined" | "retired";

/** Filter and sort order of the admission states: usable first. */
export const ADMISSION_STATES: readonly AdmissionState[] = ["admitted", "stale", "draft", "not_locked", "quarantined", "retired"];

/** Difficulty order; an unset difficulty sorts last. */
export const DIFFICULTIES = ["easy", "medium", "hard"] as const;

/** PS2 failure families in display order. */
export const PS2_FAMILIES: readonly PS2Family[] = ["unavailable_model", "broken_tool", "misleading_instruction"];

/**
 * Controls the bring-your-own contract says admission must prove, always listed in the checklist
 * so a control the last admission did not record shows as "not recorded" instead of vanishing.
 */
export const CONTRACT_CONTROLS: readonly AdmissionControl[] = [
	"reference",
	"empty",
	"mutant",
	"exploit",
	"null_agent",
	"secrets",
	"network",
];

/** Listed only when the admission recorded them. */
export const EXTRA_CONTROLS: readonly AdmissionControl[] = ["stale_notes", "other"];

/** Tone classes from the chart tokens, so badges follow light and dark mode. */
export const TONE_CLASS = {
	good: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	warn: "border-chart-warning/50 bg-chart-warning/10 text-chart-warning-ink",
	bad: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	neutral: "border-border bg-muted text-muted-foreground",
} as const;

export type Tone = keyof typeof TONE_CLASS;

export const LOCK_TONE: Record<SuiteLockState, Tone> = { locked: "good", unlocked: "neutral", stale: "warn", unknown: "bad" };

export const ADMISSION_TONE: Record<AdmissionState, Tone> = {
	admitted: "good",
	stale: "warn",
	draft: "bad",
	not_locked: "neutral",
	quarantined: "warn",
	retired: "neutral",
};

/** A task's admission state. Admitted means usable now: every check passed and nothing drifted. */
export function admissionState(task: SuiteTask): AdmissionState {
	if (task.admitted) return "admitted";
	if (task.stale && task.status === "admitted") return "stale";
	switch (task.status) {
		case "not_locked":
		case "quarantined":
		case "retired":
			return task.status;
		default:
			return "draft";
	}
}

export type TaskSortKey = "id" | "title" | "kind" | "difficulty" | "admission" | "license" | "hidden";

export const TASK_SORT_KEYS: readonly TaskSortKey[] = ["id", "title", "kind", "difficulty", "admission", "license", "hidden"];

export const SORT_DIRS = ["asc", "desc"] as const;

export type SortDir = (typeof SORT_DIRS)[number];

export interface TaskSort {
	key: TaskSortKey;
	dir: SortDir;
}

export const DEFAULT_TASK_SORT: TaskSort = { key: "id", dir: "asc" };

/** The sort from URL values; anything unknown falls back to the default. */
export function parseTaskSort(key: string | null, dir: string | null): TaskSort {
	const k = TASK_SORT_KEYS.find((candidate) => candidate === key) ?? DEFAULT_TASK_SORT.key;
	const d = dir === "desc" ? "desc" : "asc";
	return { key: k, dir: d };
}

/** A click on a header: the same column flips direction, another column starts ascending. */
export function nextTaskSort(sort: TaskSort, key: TaskSortKey): TaskSort {
	if (sort.key === key) return { key, dir: sort.dir === "asc" ? "desc" : "asc" };
	return { key, dir: "asc" };
}

/** A task's license, or null when its provenance records none. */
export function taskLicense(task: SuiteTask): string | null {
	const license = task.provenance?.license?.trim();
	return license ? license : null;
}

// The sort value of one column; null means unknown and always sorts last.
function sortValue(task: SuiteTask, key: TaskSortKey): string | number | null {
	switch (key) {
		case "id":
			return task.id;
		case "title":
			return task.title.toLowerCase();
		case "kind":
			return task.kind;
		case "difficulty": {
			const i = DIFFICULTIES.indexOf(task.difficulty as (typeof DIFFICULTIES)[number]);
			return i < 0 ? null : i;
		}
		case "admission":
			return ADMISSION_STATES.indexOf(admissionState(task));
		case "license":
			return taskLicense(task)?.toLowerCase() ?? null;
		case "hidden":
			return task.hidden_test_files;
	}
}

/** Tasks sorted by one column, stable, ties by id; unknown values last in either direction. */
export function sortTasks(tasks: readonly SuiteTask[], sort: TaskSort): SuiteTask[] {
	const sign = sort.dir === "asc" ? 1 : -1;
	return [...tasks].sort((a, b) => {
		const va = sortValue(a, sort.key);
		const vb = sortValue(b, sort.key);
		if (va === null && vb !== null) return 1;
		if (vb === null && va !== null) return -1;
		if (va !== null && vb !== null && va !== vb) {
			const cmp = typeof va === "number" && typeof vb === "number" ? va - vb : String(va).localeCompare(String(vb));
			return sign * cmp;
		}
		return a.id.localeCompare(b.id);
	});
}

export interface TaskFilters {
	kind: string | null;
	difficulty: string | null;
	capability: string | null;
	status: AdmissionState | null;
}

/** Tasks matching every set filter. */
export function filterTasks(tasks: readonly SuiteTask[], filters: TaskFilters): SuiteTask[] {
	return tasks.filter(
		(task) =>
			(!filters.kind || task.kind === filters.kind) &&
			(!filters.difficulty || task.difficulty === filters.difficulty) &&
			(!filters.capability || task.capabilities.includes(filters.capability)) &&
			(!filters.status || admissionState(task) === filters.status),
	);
}

export interface FilterOptions {
	kinds: string[];
	difficulties: string[];
	capabilities: string[];
	statuses: AdmissionState[];
}

/** The values each filter can take in this suite, so no option leads to an empty table by construction. */
export function filterOptions(tasks: readonly SuiteTask[]): FilterOptions {
	const kinds = new Set<string>();
	const difficulties = new Set<string>();
	const capabilities = new Set<string>();
	const statuses = new Set<AdmissionState>();
	for (const task of tasks) {
		if (task.kind) kinds.add(task.kind);
		if (task.difficulty) difficulties.add(task.difficulty);
		for (const capability of task.capabilities) capabilities.add(capability);
		statuses.add(admissionState(task));
	}
	const byDifficulty = (a: string, b: string) => {
		const ia = DIFFICULTIES.indexOf(a as (typeof DIFFICULTIES)[number]);
		const ib = DIFFICULTIES.indexOf(b as (typeof DIFFICULTIES)[number]);
		return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
	};
	return {
		kinds: [...kinds].sort(),
		difficulties: [...difficulties].sort(byDifficulty),
		capabilities: [...capabilities].sort(),
		statuses: ADMISSION_STATES.filter((state) => statuses.has(state)),
	};
}

export interface CountRow {
	name: string;
	count: number;
}

/** Tasks per capability, most covered first, and how many tasks name none. */
export function capabilityCoverage(tasks: readonly SuiteTask[]): { rows: CountRow[]; missing: number } {
	const counts = new Map<string, number>();
	let missing = 0;
	for (const task of tasks) {
		if (task.capabilities.length === 0) missing++;
		for (const capability of new Set(task.capabilities)) counts.set(capability, (counts.get(capability) ?? 0) + 1);
	}
	return { rows: sortedCounts(counts), missing };
}

/** Tasks per license, most common first, and the ids of tasks with none. */
export function licenseSummary(tasks: readonly SuiteTask[]): { rows: CountRow[]; missing: string[] } {
	const counts = new Map<string, number>();
	const missing: string[] = [];
	for (const task of tasks) {
		const license = taskLicense(task);
		if (license === null) missing.push(task.id);
		else counts.set(license, (counts.get(license) ?? 0) + 1);
	}
	return { rows: sortedCounts(counts), missing };
}

function sortedCounts(counts: Map<string, number>): CountRow[] {
	return [...counts.entries()].map(([name, count]) => ({ name, count })).sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
}

/** Probes grouped by PS2 family, in PS2_FAMILIES order; an unknown family is kept, after them. */
export function probesByFamily(probes: readonly SuiteProbe[]): { family: string; probes: SuiteProbe[] }[] {
	const groups = new Map<string, SuiteProbe[]>();
	for (const probe of probes) groups.set(probe.ps2_family, [...(groups.get(probe.ps2_family) ?? []), probe]);
	const order = [...PS2_FAMILIES, ...[...groups.keys()].filter((family) => !PS2_FAMILIES.includes(family as PS2Family)).sort()];
	return order.filter((family) => groups.has(family)).map((family) => ({ family, probes: groups.get(family) ?? [] }));
}

export interface ControlRow {
	control: AdmissionControl;
	checks: SuiteAdmissionCheck[];
	passed: number;
	/** True when the last admission recorded at least one check of this control. */
	recorded: boolean;
}

/** The admission checklist: every contract control, then extra ones that were recorded. */
export function controlRows(checks: readonly SuiteAdmissionCheck[]): ControlRow[] {
	const byControl = new Map<AdmissionControl, SuiteAdmissionCheck[]>();
	for (const check of checks) byControl.set(check.control, [...(byControl.get(check.control) ?? []), check]);
	const controls = [...CONTRACT_CONTROLS, ...EXTRA_CONTROLS.filter((control) => byControl.has(control))];
	return controls.map((control) => {
		const list = byControl.get(control) ?? [];
		return { control, checks: list, passed: list.filter((check) => check.passed).length, recorded: list.length > 0 };
	});
}

/** The suite shown when the URL names none: the first one that loaded, else the first. */
export function defaultSuiteId(suites: readonly SuiteSummary[]): string | null {
	return (suites.find((suite) => !suite.error) ?? suites[0])?.id ?? null;
}

/** The leaderboard's suite parameter for a suite: id@version. */
export function leaderboardSuite(suite: Pick<SuiteSummary, "id" | "version">): string {
	return `${suite.id}@${suite.version}`;
}

/** A byte count for people: 512 B, 4.1 KB, 2.3 MB. */
export function formatBytes(bytes: number, locale: string): string {
	if (bytes < 1024) return `${bytes.toLocaleString(locale)} B`;
	const units = ["KB", "MB", "GB"];
	let value = bytes / 1024;
	let unit = 0;
	while (value >= 1024 && unit < units.length - 1) {
		value /= 1024;
		unit++;
	}
	return `${value.toLocaleString(locale, { maximumFractionDigits: 1 })} ${units[unit]}`;
}