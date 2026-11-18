import type { LiveResult, LiveSeriesBucket, LiveSignalDef, LiveSignalResult } from "./livescore.types";

/** Badge variant of a comparison result: worse is the one that needs attention. */
export function resultVariant(result: string): "destructive" | "success" | "secondary" {
	if (result === "worse") return "destructive";
	if (result === "better") return "success";
	return "secondary";
}

const RESULTS: readonly LiveResult[] = ["better", "worse", "not_yet_known"];

/** Whether the server sent a result this UI knows. */
export function isLiveResult(v: string): v is LiveResult {
	return (RESULTS as readonly string[]).includes(v);
}

/** One signal value for display: a rate as a percentage, an ordinal in its unit; unknown as a dash. */
export function formatSignal(value: number | undefined, kind: string, unit?: string): string {
	if (value === undefined || value === null || Number.isNaN(value)) return "-";
	if (kind === "rate") return `${(value * 100).toFixed(1)}%`;
	if (unit === "USD") return `$${value.toFixed(4)}`;
	if (unit === "ms") return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value.toFixed(1)} ms`;
	if (unit) return `${Math.round(value).toLocaleString("en-US")} ${unit}`;
	return value.toFixed(2);
}

/** An e-value for display: large ones in scientific notation, so 3.6e8 reads as such. */
export function formatE(e: number): string {
	if (!Number.isFinite(e)) return "-";
	if (e >= 10000) return e.toExponential(1);
	return e.toFixed(2);
}

/** The comparison rows, decided signals first (worse, then better), then by evidence. */
export function sortSignalResults(rows: readonly LiveSignalResult[]): LiveSignalResult[] {
	const rank = (r: LiveSignalResult) => (r.decision === "worse" ? 0 : r.decision === "better" ? 1 : 2);
	return [...rows].sort((a, b) => rank(a) - rank(b) || Math.max(b.e_worse, b.e_better) - Math.max(a.e_worse, a.e_better));
}

/** A signal's values over a version's series, null where the bucket had none known. */
export function seriesValues(series: readonly LiveSeriesBucket[], signal: string): (number | null)[] {
	return series.map((b) => b.means.find((m) => m.signal === signal)?.mean ?? null);
}

/**
 * Points of a sparkline: x spread over width, y scaled to [0, height] from the shared range of every
 * version's values (so two versions' lines compare), gaps for unknown values. Rates use 0 to 1.
 */
export function sparkPoints(values: readonly (number | null)[], lo: number, hi: number, width: number, height: number): string {
	if (values.length === 0) return "";
	const span = hi - lo || 1;
	const step = values.length > 1 ? width / (values.length - 1) : 0;
	const out: string[] = [];
	values.forEach((v, i) => {
		if (v === null) return;
		const y = height - ((v - lo) / span) * height;
		out.push(`${(i * step).toFixed(1)},${Math.max(0, Math.min(height, y)).toFixed(1)}`);
	});
	return out.join(" ");
}

/** The shared y range of a signal over several series. */
export function seriesRange(all: readonly (number | null)[][], def: LiveSignalDef): [number, number] {
	if (def.kind === "rate") return [0, 1];
	let lo = Infinity;
	let hi = -Infinity;
	for (const vs of all) {
		for (const v of vs) {
			if (v === null) continue;
			lo = Math.min(lo, v);
			hi = Math.max(hi, v);
		}
	}
	if (!Number.isFinite(lo)) return [0, 1];
	return [Math.min(0, lo), hi];
}

/** The inbox page with one item open. */
export function inboxLink(item: string): string {
	return `/inbox?item=${encodeURIComponent(item)}`;
}
