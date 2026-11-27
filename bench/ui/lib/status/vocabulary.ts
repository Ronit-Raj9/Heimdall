import type { StatusTone } from "@/components/ui/statusPill";
import { CircleCheck, CircleHelp, CircleSlash, CircleStop, CircleX, Clock, Hand, Loader, type LucideIcon } from "lucide-react";

/**
 * One status vocabulary for the whole app (Spec 4). Every raw state a server object can be in maps
 * to one of eight words, one tone and one icon, so a cancelled job, a cancelled evaluation and a
 * cancelled comparison read and look the same everywhere.
 */
export type StatusState = "waiting" | "running" | "needsYou" | "finished" | "failed" | "stopped" | "broken" | "noAnswer";

export interface StatusInfo {
	state: StatusState;
	labelKey: `status:${StatusState}`;
	tone: StatusTone;
	icon: LucideIcon;
}

const RAW: Readonly<Record<StatusState, readonly string[]>> = {
	waiting: ["queued", "created", "planned", "pending", "new", "not_run", "no_baseline", "untested", "never", "waiting"],
	running: [
		"running",
		"analyzing",
		"building",
		"starting",
		"checking",
		"retrying",
		"labelling",
		"learning",
		"stopping",
		"rules_drafted",
		"watching",
	],
	needsYou: ["awaiting_approval", "needs_files", "pending_review", "disputed", "rules_to_approve", "flagged", "needs_lock", "stale", "past_due", "needs_you"],
	finished: ["done", "succeeded", "success", "built", "validated", "delivered", "imported", "accepted", "steady", "healthy", "finished", "completed", "pass", "passed", "solved", "ok", "pinned", "clear"],
	failed: ["failed", "error", "fail", "down", "refused", "lost", "invalid_key", "unpaid", "timeout", "budget", "crash", "refusal", "integrity_violation"],
	stopped: ["cancelled", "canceled", "stopped", "interrupted", "budget_stop", "paused", "quarantined", "retired", "revoked", "snoozed"],
	broken: ["invalid", "broken", "replay_infrastructure_failure", "baseline_stale", "infra_refusal", "infrastructure", "infra_error", "aborted_infra", "invalid_evaluator"],
	noAnswer: ["inconclusive", "cannot_tell", "insufficient_sample", "no_data", "not_enough_data", "unknown", "tie"],
};

const TONE: Readonly<Record<StatusState, StatusTone>> = {
	waiting: "neutral",
	running: "running",
	needsYou: "warn",
	finished: "ok",
	failed: "bad",
	stopped: "neutral",
	broken: "warn",
	noAnswer: "neutral",
};

const ICON: Readonly<Record<StatusState, LucideIcon>> = {
	waiting: Clock,
	running: Loader,
	needsYou: Hand,
	finished: CircleCheck,
	failed: CircleX,
	stopped: CircleStop,
	broken: CircleSlash,
	noAnswer: CircleHelp,
};

const BY_RAW: ReadonlyMap<string, StatusState> = new Map(
	(Object.entries(RAW) as [StatusState, readonly string[]][]).flatMap(([state, raws]) => raws.map((raw) => [raw, state] as const)),
);

/** The raw values a state covers, for filters ("show only failed"). */
export function rawValuesOf(state: StatusState): readonly string[] {
	return RAW[state];
}

/** Normalises a raw status: lower case, spaces and dashes as underscores, camelCase split. */
function normal(raw: string): string {
	return raw
		.trim()
		.replace(/([a-z])([A-Z])/g, "$1_$2")
		.toLowerCase()
		.replace(/[\s-]+/g, "_");
}

/** The state, word key, tone and icon of a raw status. Unknown values read "No clear result yet". Pure. */
export function statusOf(raw: string | null | undefined): StatusInfo {
	const state = (raw && BY_RAW.get(normal(raw))) || "noAnswer";
	return { state, labelKey: `status:${state}`, tone: TONE[state], icon: ICON[state] };
}

/** Readiness of a task or benchmark: "Ready" or "Not ready" (never used for a worker, which is "Online"). */
export type ReadinessState = "ready" | "notReady";
export function readinessOf(ready: boolean): { state: ReadinessState; labelKey: `status:readiness.${ReadinessState}`; tone: StatusTone } {
	return ready ? { state: "ready", labelKey: "status:readiness.ready", tone: "ok" } : { state: "notReady", labelKey: "status:readiness.notReady", tone: "neutral" };
}

/** Outcome of comparing B with A. */
export type VerdictState = "better" | "worse" | "same" | "tooFew";
const VERDICT_RAW: Readonly<Record<string, VerdictState>> = {
	improved: "better",
	better: "better",
	regressed: "worse",
	worse: "worse",
	same: "same",
	tie: "same",
	inconclusive: "tooFew",
	insufficient_sample: "tooFew",
	too_few: "tooFew",
};
export function verdictOf(raw: string | null | undefined): { state: VerdictState; labelKey: `status:verdict.${VerdictState}`; tone: StatusTone } {
	const state = (raw && VERDICT_RAW[normal(raw)]) || "tooFew";
	const tone: StatusTone = state === "better" ? "ok" : state === "worse" ? "bad" : "neutral";
	return { state, labelKey: `status:verdict.${state}`, tone };
}
