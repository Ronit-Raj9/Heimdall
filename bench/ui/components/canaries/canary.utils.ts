import type { CanaryDetail, CanaryRecord, CanaryState, CanarySummary, CanaryTarget, CanaryTestStatus } from "@/lib/types/bench";

/** Badge variants the shared Badge knows. */
export type CanaryBadgeVariant = "default" | "secondary" | "destructive" | "outline" | "success" | "warning";

export const CANARY_STATES: readonly CanaryState[] = ["no_baseline", "steady", "watching", "drifted", "invalid", "baseline_stale"];

export function isCanaryState(s: string): s is CanaryState {
	return (CANARY_STATES as readonly string[]).includes(s);
}

/** drifted is the alarm; watching and a stale baseline need a look; invalid is a harness problem. */
export function stateVariant(state: string): CanaryBadgeVariant {
	switch (state) {
		case "steady":
			return "success";
		case "drifted":
			return "destructive";
		case "watching":
		case "baseline_stale":
			return "warning";
		case "invalid":
			return "outline";
		default:
			return "secondary";
	}
}

const TEST_STATUSES: readonly CanaryTestStatus[] = ["worse", "better", "below_floor", "no_change", "cannot_tell"];

export function isTestStatus(s: string): s is CanaryTestStatus {
	return (TEST_STATUSES as readonly string[]).includes(s);
}

export function testStatusVariant(status: string): CanaryBadgeVariant {
	switch (status) {
		case "worse":
			return "destructive";
		case "better":
			return "success";
		case "below_floor":
			return "warning";
		default:
			return "secondary";
	}
}

/** Order for the list: the alarm first, then what needs a look, then the rest, each by name. */
const STATE_RANK: Record<CanaryState, number> = { drifted: 0, watching: 1, invalid: 2, baseline_stale: 3, no_baseline: 4, steady: 5 };

export function sortCanaries(list: readonly CanarySummary[]): CanarySummary[] {
	return [...list].sort((a, b) => (STATE_RANK[a.state] ?? 9) - (STATE_RANK[b.state] ?? 9) || a.name.localeCompare(b.name));
}

export function targetLabel(t: CanaryTarget): string {
	return t.provider ? `${t.provider}/${t.model}` : t.model;
}

/** The newest record that compared against the baseline (it carries tests and what changed). */
export function latestComparedLook(detail: Pick<CanaryDetail, "records">): CanaryRecord | null {
	return detail.records.find((r) => r.comparison !== undefined && r.comparison !== null) ?? null;
}

/** The newest record with an equivalence report. */
export function latestEquivalence(detail: Pick<CanaryDetail, "records">): CanaryRecord | null {
	return detail.records.find((r) => (r.equivalence ?? []).length > 0) ?? null;
}

/** A share as a percentage, or null. */
export function formatShare(v: number | null | undefined, locale: string, digits = 0): string | null {
	if (v === null || v === undefined || !Number.isFinite(v)) return null;
	return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: digits }).format(v);
}

/** A signed relative change: "+60%". */
export function formatShift(v: number | null | undefined, locale: string): string | null {
	if (v === null || v === undefined || !Number.isFinite(v)) return null;
	const s = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0, signDisplay: "exceptZero" }).format(v);
	return s;
}

/** A metric value in its unit. */
export function formatMetricValue(metric: string, v: number | null | undefined, locale: string): string | null {
	if (v === null || v === undefined || !Number.isFinite(v)) return null;
	switch (metric) {
		case "verified_success":
			return formatShare(v, locale);
		case "cost_usd":
			return new Intl.NumberFormat(locale, { style: "currency", currency: "USD", maximumFractionDigits: 4 }).format(v);
		case "latency_ms":
			return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(v)} ms`;
		default:
			return new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(v);
	}
}

/** A p-value for a table cell: "0.0078", "< 0.0001", or null when not tested. */
export function formatP(p: number | null | undefined): string | null {
	if (p === null || p === undefined || !Number.isFinite(p)) return null;
	if (p < 0.0001) return "< 0.0001";
	return p.toPrecision(2);
}

export function formatUSD(v: number, locale: string): string {
	return new Intl.NumberFormat(locale, { style: "currency", currency: "USD", maximumFractionDigits: 2 }).format(v);
}