import type { Action, ExperimentStatus, ReleasePolicyDecision, ReleaseRecord } from "@/lib/types/bench";

/**
 * The experiments a release can cite: only finished ones. Picking a running experiment would ask the
 * server for its verdict, and the server analyzes on demand and saves that analysis into the running
 * experiment's folder (a partial, usually invalid, verdict).
 */
export function releasableExperiments<T extends Pick<ExperimentStatus, "state">>(experiments: readonly T[]): T[] {
	return experiments.filter((e) => e.state === "done");
}

/** Records newest first. The API returns log order, oldest first; equal times keep the later log line first. */
export function newestFirst(releases: readonly ReleaseRecord[]): ReleaseRecord[] {
	const at = (r: ReleaseRecord) => {
		const t = Date.parse(r.created_at);
		return Number.isNaN(t) ? Number.NEGATIVE_INFINITY : t;
	};
	return releases
		.map((record, index) => ({ record, index }))
		.sort((x, y) => at(y.record) - at(x.record) || y.index - x.index)
		.map(({ record }) => record);
}

/**
 * Per record: does its prev_hash name the hash of the record before it in log order? This is the link
 * the UI can check alone; the server's chain_ok also recomputes every record hash.
 */
export function chainLinks(releases: readonly ReleaseRecord[]): Record<string, boolean> {
	const links: Record<string, boolean> = {};
	releases.forEach((record, i) => {
		links[record.release_id] = record.prev_hash === (i === 0 ? "" : releases[i - 1].hash);
	});
	return links;
}

/** The first `n` hex characters of a hash, without a "sha256:" prefix. */
export function shortHash(hash: string, n = 12): string {
	return hash.replace(/^sha256:/, "").slice(0, n);
}

/** A UTC timestamp that renders the same on every machine. */
export function formatReleaseTime(iso: string): string {
	const t = Date.parse(iso);
	if (Number.isNaN(t)) return iso;
	return `${new Date(t).toISOString().slice(0, 16).replace("T", " ")} UTC`;
}

/** Why a release needs an override. */
export type ReleaseBlocker = "notShip" | "policyBlock";

/** What stands between the verdict and a release: a non-ship action, a policy block, or nothing. */
export function releaseBlockers(action: Action | undefined, policy: ReleasePolicyDecision | undefined): ReleaseBlocker[] {
	const blockers: ReleaseBlocker[] = [];
	if (action !== "ship") blockers.push("notShip");
	if (policy === "block") blockers.push("policyBlock");
	return blockers;
}

export interface ReleaseFormValues {
	approver: string;
	note: string;
	/** The explicit "override with reason" checkbox. Only offered when a blocker exists. */
	override: boolean;
	overrideReason: string;
}

export const EMPTY_RELEASE_FORM: ReleaseFormValues = { approver: "", note: "", override: false, overrideReason: "" };

/**
 * The release button is enabled when a verdict is loaded, an approver is named, and either nothing
 * blocks (action ship, policy not block) or the override box is ticked with a reason.
 */
export function canRelease(
	verdictLoaded: boolean,
	action: Action | undefined,
	policy: ReleasePolicyDecision | undefined,
	values: ReleaseFormValues,
): boolean {
	if (!verdictLoaded || values.approver.trim() === "") return false;
	if (releaseBlockers(action, policy).length === 0) return true;
	return values.override && values.overrideReason.trim() !== "";
}

/** The request for the form; the override reason is sent only when the release needs and has one. */
export function releaseRequestFrom(
	expId: string,
	analysisId: string | undefined,
	blocked: boolean,
	values: ReleaseFormValues,
): { exp_id: string; analysis_id?: string; approver: string; note?: string; override?: string } {
	const note = values.note.trim();
	const override = blocked && values.override ? values.overrideReason.trim() : "";
	return {
		exp_id: expId,
		...(analysisId ? { analysis_id: analysisId } : {}),
		approver: values.approver.trim(),
		...(note ? { note } : {}),
		...(override ? { override } : {}),
	};
}

const POLICY_DECISIONS: readonly ReleasePolicyDecision[] = ["allow", "warn", "block"];

/** The policy decision a verdict response carries (its optional `policy` field), or undefined when none was evaluated. */
export function verdictPolicyDecision(verdict: unknown): ReleasePolicyDecision | undefined {
	if (!verdict || typeof verdict !== "object") return undefined;
	const policy = (verdict as { policy?: unknown }).policy;
	if (!policy || typeof policy !== "object") return undefined;
	const decision = (policy as { decision?: unknown }).decision;
	return POLICY_DECISIONS.find((d) => d === decision);
}