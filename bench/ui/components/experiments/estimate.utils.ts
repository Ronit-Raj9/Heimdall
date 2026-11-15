import { formatValueNumber } from "@/components/valueCell.utils";
import type { Tone } from "@/components/verdict/verdict.utils";
import type { ForecastFit, ForecastRange, ValueKind } from "@/lib/types/bench";

// Pure helpers for the estimate box (POST /estimate, docs/forecast.md), tested without rendering.

/** What one forecast range shows, with its numbers already formatted for the locale. */
export type RangeView =
	| { kind: "history"; low: string; median: string; high: string }
	| { kind: "upper_bound"; high: string }
	| { kind: "unknown"; reason: string | null };

/**
 * Turns a forecast range into what the box shows. A number the kind needs but the server left
 * null makes the range unknown: a null is never drawn as 0.
 */
export function forecastRangeView(range: ForecastRange | null | undefined, kind: ValueKind, locale: string): RangeView {
	if (!range) return { kind: "unknown", reason: null };
	const format = (v: number | null | undefined) =>
		typeof v === "number" && Number.isFinite(v) ? formatValueNumber(v, kind, locale) : null;
	const low = format(range.low);
	const median = format(range.median);
	const high = format(range.high);
	if (range.kind === "history" && low !== null && median !== null && high !== null) return { kind: "history", low, median, high };
	if (range.kind === "upper_bound" && high !== null) return { kind: "upper_bound", high };
	return { kind: "unknown", reason: range.reason?.trim() || null };
}

/** Badge tone per budget fit. Never green unless even the high end fits. */
export const FIT_TONE = {
	fits: "good",
	may_exceed: "warn",
	likely_exceeds: "bad",
	exceeds: "bad",
	unknown: "neutral",
	no_budget: "neutral",
} as const satisfies Record<ForecastFit, Tone>;

/** The fit as a known value; anything the UI does not know reads as "unknown". */
export function forecastFit(fit: string): ForecastFit {
	return fit in FIT_TONE ? (fit as ForecastFit) : "unknown";
}