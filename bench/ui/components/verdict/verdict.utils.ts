import type { Decision, ExperimentStatus, GridRow, GuardStatus, Outcome, Ratio, Value, Verdict, VerdictPayload } from "@/lib/types/bench";

// Pure helpers for the verdict screen, so the formatting and ordering rules are tested without
// rendering. Numbers follow the UI locale; callers pass getLocale().

/** Visual tone shared by the decision label, guard badges and integrity. Never green for a regression. */
export type Tone = "good" | "bad" | "warn" | "neutral";

export const DECISION_TONE = {
	improved: "good",
	regressed: "bad",
	inconclusive: "warn",
	invalid: "neutral",
} as const satisfies Record<Decision, Tone>;

export const GUARD_TONE = {
	pass: "good",
	fail: "bad",
	undetermined: "warn",
} as const satisfies Record<GuardStatus, Tone>;

/** Tailwind classes per tone. Status tokens from globals.css, so light and dark both hold contrast. */
export const TONE_CLASS = {
	good: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	bad: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	warn: "border-chart-warning/60 bg-chart-warning/10 text-chart-warning-ink",
	neutral: "border-border bg-muted text-muted-foreground",
} as const satisfies Record<Tone, string>;

/** Text colour alone per tone, for icons and inline numbers. */
export const TONE_TEXT = {
	good: "text-chart-success-ink",
	bad: "text-chart-error-ink",
	warn: "text-chart-warning-ink",
	neutral: "text-muted-foreground",
} as const satisfies Record<Tone, string>;

/** Integrity as sent by the server. Go also sends "unknown" (audit verdicts); "" means not assessed. */
export type IntegrityView = "clean" | "alert" | "regressed" | "unknown" | "none";

export function integrityView(integrity: string | undefined): IntegrityView {
	switch (integrity) {
		case "clean":
		case "alert":
		case "regressed":
		case "unknown":
			return integrity;
		default:
			return "none";
	}
}

/**
 * Integrity tone. "regressed" is red. "clean" is green only when the verdict itself did not
 * regress: a regressed release never shows green beside it (DESIGN.md 9.4).
 */
export function integrityTone(integrity: IntegrityView, decision: Decision): Tone {
	switch (integrity) {
		case "regressed":
			return "bad";
		case "alert":
			return "warn";
		case "clean":
			return decision === "regressed" ? "neutral" : "good";
		default:
			return "neutral";
	}
}

/** Tasks a new experiment needs: an exact count, or "more than" when tasks_needed_over is set. */
export type TasksNeededView = { kind: "exact"; count: number } | { kind: "over"; count: number } | null;

export function tasksNeededView(verdict: Verdict): TasksNeededView {
	const over = verdict.tasks_needed_over;
	if (typeof over === "number" && over > 0) return { kind: "over", count: over };
	if (typeof verdict.tasks_needed === "number" && verdict.tasks_needed > 0) return { kind: "exact", count: verdict.tasks_needed };
	return null;
}

/** A fraction (0.4167) as signed percentage points ("+41.7"). Zero has no sign. */
export function formatSignedPp(fraction: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 1, signDisplay: "exceptZero" }).format(fraction * 100);
}

/** Unit suffix a non-rate metric name carries (_usd, _ms, _s), or null for a rate read in pp. Mirrors unitText in reporters/markdown.go. */
export function metricUnit(metric: string | undefined): "USD" | "ms" | "s" | null {
	if (!metric) return null;
	if (metric.endsWith("_usd")) return "USD";
	if (metric.endsWith("_ms")) return "ms";
	if (metric.endsWith("_s")) return "s";
	return null;
}

/** An effect difference in its metric's own unit: signed pp for rates, "-9 s" for seconds, "+0.03 USD" for spend. */
export function formatSignedEffect(metric: string | undefined, value: number, locale: string): string {
	const unit = metricUnit(metric);
	if (unit === null) return `${formatSignedPp(value, locale)} pp`;
	const n = new Intl.NumberFormat(locale, { maximumFractionDigits: unit === "USD" ? 4 : 3, signDisplay: "exceptZero" }).format(value);
	return `${n} ${unit}`;
}

/** A number already in percentage points (the MDE), unsigned. */
export function formatPp(pp: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(pp);
}

/** Smallest p-value printed as a number; below it the screen says "< 0.0001". */
export const P_VALUE_FLOOR = 0.0001;

/** An exact p-value with up to four decimals: exact sign-flip values (0.125, 0.0078) stay readable. */
export function formatPValue(p: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 4 }).format(p);
}

export function formatSd(sd: number, locale: string): string {
	return new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(sd);
}

export function formatCount(n: number, locale: string): string {
	return new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(n);
}

/** "sha256:52b5454d...9338" as "52b5454d…9338"; the full hash goes in the title. */
export function shortHash(hash: string): string {
	const hex = hash.includes(":") ? hash.slice(hash.indexOf(":") + 1) : hash;
	return hex.length <= 14 ? hex : `${hex.slice(0, 8)}…${hex.slice(-4)}`;
}

/** An ISO time for the UI locale, or null when it does not parse. */
export function formatDateTime(iso: string | undefined, locale: string): string | null {
	if (!iso) return null;
	const date = new Date(iso);
	if (Number.isNaN(date.getTime())) return null;
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

/** Arm A then arm B: from the primary effect when present, else the naive keys in order. */
export function armOrder(payload: Pick<VerdictPayload, "verdict" | "naive">): string[] {
	const effect = payload.verdict.effect;
	if (effect) return [effect.arm_a, effect.arm_b];
	return Object.keys(payload.naive).sort();
}

/** Who leads on raw passes, as a typical dashboard would say it. Null leader on a tie. */
export function naiveLeader(naive: Record<string, Ratio>, arms: string[]): { leader: string | null; by: number } {
	const [a, b] = arms;
	const na = naive[a]?.num ?? 0;
	const nb = naive[b]?.num ?? 0;
	if (na === nb) return { leader: null, by: 0 };
	return na > nb ? { leader: a, by: na - nb } : { leader: b, by: nb - na };
}

/** Experiments newest first, for the experiment list and the picker. */
export function sortExperimentsNewestFirst(experiments: ExperimentStatus[]): ExperimentStatus[] {
	const at = (e: ExperimentStatus) => {
		const t = Date.parse(e.frozen_at);
		return Number.isNaN(t) ? Number.NEGATIVE_INFINITY : t;
	};
	return [...experiments].sort((x, y) => at(y) - at(x));
}

/** How a grid cell is drawn. Intention-to-treat (DESIGN.md 9.3): every non-pass counted outcome is 0. */
export type CellTone = "pass" | "zero" | "integrity" | "unknown";

const ZERO_OUTCOMES: ReadonlySet<Outcome> = new Set<Outcome>(["fail", "timeout", "budget", "crash", "refusal"]);

export function cellTone(outcome: Outcome | null, y: Value | undefined): CellTone {
	if (outcome === "integrity_violation") return "integrity";
	if (!y || y.state !== "observed" || typeof y.v !== "number") return "unknown";
	if (outcome === "pass") return "pass";
	if (outcome && ZERO_OUTCOMES.has(outcome)) return "zero";
	return "unknown";
}

/** Discordant rows first; otherwise the server's order is kept. */
export function sortGridRows(rows: GridRow[]): GridRow[] {
	return rows
		.map((row, index) => ({ row, index }))
		.sort((x, y) => Number(y.row.discordant) - Number(x.row.discordant) || x.index - y.index)
		.map(({ row }) => row);
}

/** Group of an always-on meter (DESIGN.md 9.6). The Meter type has no group field, so it is keyed by name. */
export type MeterGroup = "honesty" | "safety" | "efficiency" | "other";

const METER_GROUPS: Record<string, MeterGroup> = {
	false_completion: "honesty",
	claim_precision: "honesty",
	safe_success: "honesty",
	gates_fired: "safety",
	egress_attempts: "safety",
	cost_per_verified_success: "efficiency",
};

export function meterGroup(name: string): MeterGroup {
	return METER_GROUPS[name] ?? "other";
}

export const METER_GROUP_ORDER: readonly MeterGroup[] = ["honesty", "safety", "efficiency", "other"];

/** Per-arm medians shown in the efficiency meter (DESIGN.md 9.6), in this order when present. */
export const EFFICIENCY_ARM_METRICS: readonly string[] = ["steps", "tool_calls", "tokens", "cost_usd", "wall_time_s"];

/** True when the missingness bounds of an effect differ from its point estimate. */
export function boundsDiffer(effect: { d_bar: number; lower_bound?: { d_bar: number }; upper_bound?: { d_bar: number } }): boolean {
	const lo = effect.lower_bound?.d_bar;
	const hi = effect.upper_bound?.d_bar;
	if (lo === undefined || hi === undefined) return false;
	return Math.abs(lo - effect.d_bar) > 1e-9 || Math.abs(hi - effect.d_bar) > 1e-9;
}