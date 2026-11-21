import type { StatusTone } from "@/components/ui/statusPill";
import type { Evaluation, EvaluationArm, EvaluationChild } from "@/lib/store/apis/evaluationsApi";

/** How often a running evaluation (and its running benchmarks) is polled. */
export const EVALUATION_POLL_MS = 3000;

const KNOWN_MODES = ["score", "versions", "models", "opponent"] as const;
const KNOWN_STATUSES = ["queued", "created", "running", "analyzing", "done", "failed", "cancelled", "skipped"] as const;

/** The i18n key (evaluations namespace) of a mode in plain words. */
export function modeKey(mode: string): `mode.${(typeof KNOWN_MODES)[number]}` | null {
	return (KNOWN_MODES as readonly string[]).includes(mode) ? (`mode.${mode}` as `mode.${(typeof KNOWN_MODES)[number]}`) : null;
}

/** The i18n key of a status word, or null for a status this UI does not know. */
export function statusKey(status: string): `status.${(typeof KNOWN_STATUSES)[number]}` | null {
	return (KNOWN_STATUSES as readonly string[]).includes(status)
		? (`status.${status}` as `status.${(typeof KNOWN_STATUSES)[number]}`)
		: null;
}

/** Still moving: keep polling. */
export function isActiveStatus(status: string): boolean {
	return status === "queued" || status === "created" || status === "running" || status === "analyzing";
}

export function statusTone(status: string): StatusTone {
	switch (status) {
		case "done":
			return "ok";
		case "failed":
			return "bad";
		case "cancelled":
		case "skipped":
			return "warn";
		case "running":
		case "analyzing":
			return "running";
		default:
			return "neutral";
	}
}

/** "codex v2" or "codex" when no version is pinned. */
export function armAgentLabel(arm: EvaluationArm): string {
	return arm.version ? `${arm.agent} v${arm.version}` : arm.agent;
}

/** The distinct agents of the arms, in order, for the list's Agent column. */
export function evaluationAgents(evaluation: Evaluation): string[] {
	const out: string[] = [];
	for (const arm of evaluation.spec.arms ?? []) {
		const label = armAgentLabel(arm);
		if (!out.includes(label)) out.push(label);
	}
	return out;
}

/** Children that were run (have an experiment) and children that were skipped, apart. */
export function splitChildren(children: readonly EvaluationChild[] | null | undefined): {
	run: EvaluationChild[];
	skipped: EvaluationChild[];
} {
	const run: EvaluationChild[] = [];
	const skipped: EvaluationChild[] = [];
	for (const child of children ?? []) {
		if (child.skipped || !child.experiment_id) skipped.push(child);
		else run.push(child);
	}
	return { run, skipped };
}

/** Benchmarks count: the children when present, else the requested list. */
export function benchmarkCount(evaluation: Evaluation): number {
	const children = evaluation.children ?? [];
	return children.length > 0 ? children.length : (evaluation.spec.benchmarks ?? []).length;
}

/** Newest first, whatever order the server sent. */
export function sortNewestFirst(list: readonly Evaluation[]): Evaluation[] {
	return [...list].sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? ""));
}

/** A date in the viewer's locale; the raw text when it does not parse. */
export function formatCreated(iso: string | undefined, locale?: string): string {
	if (!iso) return "";
	const d = new Date(iso);
	return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(locale, { dateStyle: "medium", timeStyle: "short" });
}