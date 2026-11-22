import type { ReferenceEntry } from "@/lib/store/apis/referenceApi";

/** The key a benchmark id is matched on: no "@version" pin, lower case (as the server matches). */
export function referenceKey(id: string): string {
	return id.split("@")[0].trim().toLowerCase();
}

/** Entries grouped by benchmark, groups and rows in board order (the server sorts them). */
export function groupByBenchmark(entries: readonly ReferenceEntry[]): { benchmark: string; entries: ReferenceEntry[] }[] {
	const out: { benchmark: string; entries: ReferenceEntry[] }[] = [];
	const at = new Map<string, number>();
	for (const e of entries) {
		const k = referenceKey(e.benchmark);
		const i = at.get(k);
		if (i === undefined) {
			at.set(k, out.length);
			out.push({ benchmark: e.benchmark, entries: [e] });
		} else out[i].entries.push(e);
	}
	return out;
}

/** Entries by benchmark key, for the evaluate wizard's per-benchmark hint. */
export function entriesByBenchmark(entries: readonly ReferenceEntry[]): Map<string, ReferenceEntry[]> {
	const out = new Map<string, ReferenceEntry[]>();
	for (const e of entries) {
		const k = referenceKey(e.benchmark);
		out.set(k, [...(out.get(k) ?? []), e]);
	}
	return out;
}

/** Whole percent, "42%". */
export function percent(x: number): string {
	return `${Math.round(x * 100)}%`;
}

/** The agent as the board names it: id, saved version, declared build. */
export function agentName(e: Pick<ReferenceEntry, "agent" | "version" | "agent_build">): string {
	let s = e.agent;
	if (e.version) s += ` v${e.version}`;
	if (e.agent_build) s += ` (${e.agent_build})`;
	return s;
}

/** Dollars per solved task; small amounts keep more digits so they never read as $0.00. */
export function costPerSolved(x: number): string {
	if (x >= 10) return `$${Math.round(x).toLocaleString("en-US")}`;
	if (x >= 0.1) return `$${x.toFixed(2)}`;
	return `$${x.toFixed(4)}`;
}

/** First 12 hex digits of a sha256 fingerprint. */
export function shortHash(h: string): string {
	return h.replace(/^sha256:/, "").slice(0, 12);
}

export interface ReferenceHint {
	kind: "one" | "many";
	agent: string;
	score: string;
	low: string;
	high: string;
}

/**
 * What the wizard says about one benchmark: the one known score, or the spread of known scores
 * named after the best one. null when nothing is published for it (no placeholder numbers).
 */
export function referenceHint(entries: readonly ReferenceEntry[] | undefined): ReferenceHint | null {
	if (!entries || entries.length === 0) return null;
	const best = entries.reduce((a, b) => (b.score > a.score ? b : a));
	const scores = entries.map((e) => e.score);
	const low = Math.min(...scores);
	const high = Math.max(...scores);
	const kind = entries.length === 1 || percent(low) === percent(high) ? "one" : "many";
	return { kind, agent: best.agent, score: percent(best.score), low: percent(low), high: percent(high) };
}
