import type { EvidenceLevel, Value, ValueKind } from "@/lib/types/bench";

/** What the value cell draws. Pure, so the formatting rules are tested without rendering. */
export type ValueView =
	| { state: "observed"; text: string; evidence?: EvidenceLevel }
	| { state: "unknown"; reason: string | null; bounds: [string, string] | null }
	| { state: "not_applicable" };

/**
 * Formats one number of the given kind for the locale.
 * rate: a fraction shown as percent. usd: dollars. duration: seconds. count and tokens: plain counts.
 */
export function formatValueNumber(v: number, kind: ValueKind, locale: string): string {
	switch (kind) {
		case "rate":
			return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 1 }).format(v);
		case "count":
			return new Intl.NumberFormat(locale, { maximumFractionDigits: Number.isInteger(v) ? 0 : 1 }).format(v);
		case "tokens":
			return Math.abs(v) >= 10_000
				? new Intl.NumberFormat(locale, { notation: "compact", maximumFractionDigits: 1 }).format(v)
				: new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(v);
		case "usd":
			return formatUsd(v, locale);
		case "duration":
			return formatDuration(v, locale);
	}
}

/**
 * Turns a Value into what the cell shows. A missing value, or an "observed" value without a
 * number, is shown as unknown: absence is never drawn as zero.
 */
export function describeValue(value: Value | null | undefined, kind: ValueKind, locale: string): ValueView {
	if (!value) return { state: "unknown", reason: null, bounds: null };
	if (value.state === "not_applicable") return { state: "not_applicable" };
	if (value.state === "observed" && typeof value.v === "number" && Number.isFinite(value.v)) {
		return { state: "observed", text: formatValueNumber(value.v, kind, locale), evidence: value.evidence };
	}
	const bounds = value.bounds
		? ([formatValueNumber(value.bounds[0], kind, locale), formatValueNumber(value.bounds[1], kind, locale)] as [string, string])
		: null;
	return { state: "unknown", reason: value.reason ?? null, bounds };
}

function formatUsd(v: number, locale: string): string {
	// Per-call costs are fractions of a cent; two decimals would round them to $0.00.
	const small = v !== 0 && Math.abs(v) < 1;
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		...(small ? { maximumSignificantDigits: 2 } : { minimumFractionDigits: 2, maximumFractionDigits: 2 }),
	}).format(v);
}

function formatDuration(seconds: number, locale: string): string {
	const abs = Math.abs(seconds);
	const [unit, amount] =
		abs >= 3600 ? (["hour", seconds / 3600] as const) : abs >= 60 ? (["minute", seconds / 60] as const) : (["second", seconds] as const);
	return new Intl.NumberFormat(locale, { style: "unit", unit, unitDisplay: "narrow", maximumFractionDigits: 1 }).format(amount);
}