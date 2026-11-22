import type { SearchHit } from "@/lib/types/traceTools";

export interface Segment {
	text: string;
	mark: boolean;
}

/**
 * Splits text into plain and highlighted segments. Highlights are [start, end) code point
 * offsets, so the text is indexed with Array.from, never UTF-16 units. Out-of-range and
 * overlapping spans are clamped and merged.
 */
export function splitHighlights(text: string, highlights: readonly (readonly [number, number])[] | undefined): Segment[] {
	const chars = Array.from(text);
	const n = chars.length;
	const spans = (highlights ?? [])
		.map(([s, e]) => [Math.max(0, Math.min(n, Math.floor(s))), Math.max(0, Math.min(n, Math.floor(e)))] as [number, number])
		.filter(([s, e]) => e > s)
		.sort((a, b) => a[0] - b[0]);
	const merged: [number, number][] = [];
	for (const sp of spans) {
		const last = merged[merged.length - 1];
		if (last && sp[0] <= last[1]) last[1] = Math.max(last[1], sp[1]);
		else merged.push([sp[0], sp[1]]);
	}
	const out: Segment[] = [];
	let at = 0;
	for (const [s, e] of merged) {
		if (s > at) out.push({ text: chars.slice(at, s).join(""), mark: false });
		out.push({ text: chars.slice(s, e).join(""), mark: true });
		at = e;
	}
	if (at < n || out.length === 0) out.push({ text: chars.slice(at).join(""), mark: false });
	return out;
}

/** Benchmark filter for trace search: the suite id without its @version. */
export function benchmarkParam(suite: string | null | undefined): string | undefined {
	const b = (suite ?? "").replace(/@.*$/, "").trim();
	return b || undefined;
}

/** Agent label with its version when set: "support-bot v3". */
export function hitAgentLabel(hit: Pick<SearchHit, "agent" | "version">): string {
	if (!hit.version) return hit.agent;
	return /^v/i.test(hit.version) ? `${hit.agent} ${hit.version}` : `${hit.agent} v${hit.version}`;
}

/** Where a trace search hit opens on the run page (N51): a tab, and the step to select on Step by step. */
export interface HitTarget {
	tab: "overview" | "timeline" | "output";
	step?: number;
}

/**
 * The run page target of a hit: the first step match opens Step by step on that step; a tool call,
 * event or error match opens Step by step; an answer opens Output; a gate or grade reason opens
 * Overview, where they are shown. A hit with no match opens Step by step.
 */
export function hitTarget(hit: Pick<SearchHit, "matches">): HitTarget {
	const matches = hit.matches ?? [];
	const step = matches.find((m) => m.field === "step" && typeof m.seq === "number" && m.seq > 0);
	if (step) return { tab: "timeline", step: step.seq };
	const first = matches[0];
	if (!first) return { tab: "timeline" };
	if (first.field === "answer") return { tab: "output" };
	if (first.field === "gate" || first.field === "grade") return { tab: "overview" };
	return { tab: "timeline" };
}
