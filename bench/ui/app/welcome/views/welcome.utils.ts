import type { AgentSummary, SuiteSummary } from "@/lib/types/bench";

export const WIZARD_STEPS = ["suite", "agent", "model", "run", "verdict"] as const;
export type WizardStep = (typeof WIZARD_STEPS)[number];

/** The starter suite the plan pre-selects. */
export const STARTER_SUITE = "coding-core";

/** Starter agents in the order a first-time user should see them. */
const STARTER_AGENTS = ["claude-code", "codex", "mini-swe", "plain", "opencode"];

/** Run sizes offered on the Run step; 5 tasks x 1 trial is the default. */
export const RUN_SIZES = [1, 5, 10] as const;
export const DEFAULT_TASKS = 5;

/** localStorage flag: this browser has opened the wizard, so Home stops redirecting to it. */
export const WELCOME_SEEN_KEY = "heimdall.welcome.seen";

export function asStep(raw: string | null | undefined): WizardStep {
	return (WIZARD_STEPS as readonly string[]).includes(raw ?? "") ? (raw as WizardStep) : "suite";
}

/** The suite to pre-select: coding-core when present, else the first one with tasks, else the first. */
export function defaultSuite(suites: readonly SuiteSummary[]): string | undefined {
	return (suites.find((s) => s.id === STARTER_SUITE) ?? suites.find((s) => s.task_count > 0) ?? suites[0])?.id;
}

/** Suites sorted for the picker: the starter first, then by name. */
export function sortSuites(suites: readonly SuiteSummary[]): SuiteSummary[] {
	return [...suites].sort((a, b) => Number(b.id === STARTER_SUITE) - Number(a.id === STARTER_SUITE) || a.id.localeCompare(b.id));
}

function starterRank(id: string): number {
	const i = STARTER_AGENTS.findIndex((s) => id === s || id.startsWith(`${s}-`) || id.startsWith(`${s}_`));
	return i < 0 ? STARTER_AGENTS.length : i;
}

/** Agents sorted for the picker: starter agents first, scripted test doubles last. */
export function sortAgents(agents: readonly AgentSummary[]): AgentSummary[] {
	return [...agents].sort(
		(a, b) => Number(Boolean(a.scripted_kind)) - Number(Boolean(b.scripted_kind)) || starterRank(a.id) - starterRank(b.id) || a.id.localeCompare(b.id),
	);
}

export function defaultAgent(agents: readonly AgentSummary[]): string | undefined {
	return sortAgents(agents)[0]?.id;
}

/** The run size actually used: never more tasks than the suite has (when it reports a count). */
export function clampTasks(tasks: number, suite: SuiteSummary | undefined): number {
	if (!suite || suite.task_count <= 0) return tasks;
	return Math.max(1, Math.min(tasks, suite.task_count));
}

export function markWelcomeSeen(): void {
	try {
		window.localStorage.setItem(WELCOME_SEEN_KEY, "1");
	} catch {
		// Private window or blocked storage: Home may offer the wizard again, nothing breaks.
	}
}

export function welcomeSeen(): boolean {
	try {
		return window.localStorage.getItem(WELCOME_SEEN_KEY) === "1";
	} catch {
		return true;
	}
}

/** An HTTP status from an RTK Query error, when there is one. */
export function errorStatusOf(err: unknown): number | undefined {
	const s = (err as { status?: unknown } | undefined)?.status;
	return typeof s === "number" ? s : undefined;
}

/** The server's error message from an RTK Query error, or a fallback. */
export function errorMessageOf(err: unknown, fallback: string): string {
	const data = (err as { data?: unknown } | undefined)?.data as { message?: unknown; error?: { message?: unknown } } | undefined;
	const m = data?.message ?? data?.error?.message;
	return typeof m === "string" && m ? m : fallback;
}
