// Upgrade prompts (lane Y16): while billing is on, the server refuses what the org's plan does not
// cover with 402 and error type plan_limit. The bench base query reports every such answer here and
// the prompt in the app shell shows it, so a blocked action anywhere offers the upgrade.

export const PLAN_LIMIT_TYPE = "plan_limit";
export const BILLING_LINK = "/settings?tab=billing";

export interface PlanLimitNotice {
	message: string;
	link: string;
}

type Listener = (notice: PlanLimitNotice) => void;
const listeners = new Set<Listener>();

/** The plan refusal an RTK Query error carries, or null. */
export function planLimitOf(error: unknown): PlanLimitNotice | null {
	if (!error || typeof error !== "object" || (error as { status?: unknown }).status !== 402) return null;
	const body = (error as { data?: { error?: { type?: unknown; message?: unknown; link?: unknown } } }).data?.error;
	if (!body || body.type !== PLAN_LIMIT_TYPE) return null;
	const link = typeof body.link === "string" && body.link.startsWith("/") ? body.link : BILLING_LINK;
	return { message: typeof body.message === "string" ? body.message : "", link };
}

/** Reports a plan refusal to the prompt; anything else is ignored. */
export function notifyPlanLimit(error: unknown): void {
	const notice = planLimitOf(error);
	if (!notice) return;
	for (const l of listeners) l(notice);
}

/** Subscribes to plan refusals; returns the unsubscribe. */
export function onPlanLimit(listener: Listener): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}
