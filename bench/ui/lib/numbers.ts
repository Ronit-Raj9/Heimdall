// Number rules (Spec 12). Money stays in lib/money.ts (formatUsd); this file holds shares,
// ranges, durations and counts so every screen prints them the same way.

/** Wilson score interval (95 %) for k successes in n tries, as fractions. null when n is 0. */
export function wilson(k: number, n: number, z = 1.96): { low: number; high: number } | null {
	if (!Number.isFinite(k) || !Number.isFinite(n) || n <= 0) return null;
	const p = Math.min(Math.max(k / n, 0), 1);
	const z2 = z * z;
	const denom = 1 + z2 / n;
	const centre = (p + z2 / (2 * n)) / denom;
	const half = (z * Math.sqrt((p * (1 - p)) / n + z2 / (4 * n * n))) / denom;
	return { low: Math.max(0, centre - half), high: Math.min(1, centre + half) };
}

/** A percent with the precision the sample size allows: one decimal only from 100 items. */
export function formatPercent(fraction: number, n: number): string {
	const pct = fraction * 100;
	return n >= 100 ? `${pct.toFixed(1).replace(/\.0$/, "")}%` : `${Math.round(pct)}%`;
}

export interface Share {
	/** The text to print: "7 of 8", "62% (8 of 13)" or "71%". */
	text: string;
	/** Under 10 runs: too few to rate; no percent in a headline. */
	tooFew: boolean;
	/** "n = 31", shown small beside the percent from 30 items. */
	sample?: string;
	/** The likely range in words, from 10 items, when its ends differ: "likely between 35% and 85%". */
	range?: string;
}

/**
 * A share of k out of n with its sample size (Spec 12). Under `few` (10 runs; pass 5 for tasks):
 * "7 of 8" only. 10 to 29: "62% (8 of 13)" plus the range. 30 or more: the percent, "n = 31".
 */
export function shareOf(k: number, n: number, few = 10): Share {
	if (!Number.isFinite(n) || n <= 0) return { text: "-", tooFew: true };
	if (n < few) return { text: `${k} of ${n}`, tooFew: true };
	const pct = formatPercent(k / n, n);
	const w = wilson(k, n);
	const lo = w ? formatPercent(w.low, n) : "";
	const hi = w ? formatPercent(w.high, n) : "";
	const range = w && lo !== hi ? `likely between ${lo} and ${hi}` : undefined;
	if (n < 30) return { text: `${pct} (${k} of ${n})`, tooFew: false, range };
	return { text: pct, tooFew: false, sample: `n = ${n.toLocaleString("en-US")}`, range };
}

/** The short form of shareOf: the text to print in a cell. */
export function formatShare(k: number, n: number, few = 10): string {
	return shareOf(k, n, few).text;
}

/** True when two shares' ranges overlap, so ranking one above the other would claim too much: say "tied". */
export function rangesOverlap(a: { k: number; n: number }, b: { k: number; n: number }): boolean {
	const wa = wilson(a.k, a.n);
	const wb = wilson(b.k, b.n);
	if (!wa || !wb) return true;
	return wa.low <= wb.high && wb.low <= wa.high;
}

/** A duration: "850 ms", "6.2 s", "13 s", "4 min 12 s", "1 h 5 min"; "-" when unknown. */
export function formatDuration(ms: number | null | undefined): string {
	if (ms === null || ms === undefined || !Number.isFinite(ms) || ms < 0) return "-";
	if (ms < 1000) return `${Math.round(ms)} ms`;
	const s = ms / 1000;
	if (s < 10) return `${s.toFixed(1).replace(/\.0$/, "")} s`;
	if (s < 60) return `${Math.round(s)} s`;
	const totalS = Math.round(s);
	if (totalS < 3600) {
		const m = Math.floor(totalS / 60);
		const rest = totalS % 60;
		return rest ? `${m} min ${rest} s` : `${m} min`;
	}
	const totalM = Math.round(totalS / 60);
	const h = Math.floor(totalM / 60);
	const m = totalM % 60;
	return m ? `${h} h ${m} min` : `${h} h`;
}

/** A count with separators for tables: "113,351". */
export function formatCount(n: number | null | undefined): string {
	if (n === null || n === undefined || !Number.isFinite(n)) return "-";
	return Math.round(n).toLocaleString("en-US");
}

/** A compact count for tiles only: "84k", "1.2M". */
export function formatCompact(n: number | null | undefined): string {
	if (n === null || n === undefined || !Number.isFinite(n)) return "-";
	const a = Math.abs(n);
	if (a < 1000) return String(Math.round(n));
	if (a < 1_000_000) return `${(n / 1000).toFixed(a < 10_000 ? 1 : 0).replace(/\.0$/, "")}k`;
	return `${(n / 1_000_000).toFixed(a < 10_000_000 ? 1 : 0).replace(/\.0$/, "")}M`;
}
