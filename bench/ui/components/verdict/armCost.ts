import type { Value } from "@/lib/types/bench";

/** A money amount with two significant digits ("$0.10", "$0.0065"), the one format for a cost per run. */
export function formatUsd2(v: number, locale: string): string {
	return new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		minimumSignificantDigits: 2,
		maximumSignificantDigits: 2,
	}).format(v);
}

/** A number rounded to two significant digits (14.6 -> 15, 2.04 -> 2). */
export function roundSig2(n: number): number {
	if (!Number.isFinite(n) || n === 0) return n;
	return Number(n.toPrecision(2));
}

/** The observed number of a value, or null. */
export function observedNumber(v: Value | undefined): number | null {
	return v && v.state === "observed" && typeof v.v === "number" ? v.v : null;
}

/**
 * Which of two setups is much cheaper (2 times or more) and by how much, or null when a cost is
 * missing or zero, or the gap is smaller. costs holds the average cost per run of each arm.
 */
export function cheaperArm(
	arms: readonly string[],
	costs: Record<string, number | null | undefined> | undefined,
): { arm: string; ratio: number } | null {
	if (!costs || arms.length !== 2) return null;
	const [a, b] = arms;
	const ca = costs[a];
	const cb = costs[b];
	if (ca == null || cb == null || ca <= 0 || cb <= 0) return null;
	const ratio = Math.max(ca, cb) / Math.min(ca, cb);
	if (ratio < 2) return null;
	return { arm: ca < cb ? a : b, ratio: roundSig2(ratio) };
}