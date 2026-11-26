import type { StatusTone } from "@/components/ui/statusPill";
import type { ControlBand, CurvePoint, ImprovementStatus, ImprovementVerdictState, LineageNode, LineageStatus } from "@/lib/types/improve";
import { formatUsd } from "@/lib/money";

/** The three task sets in curve order, with their stroke tokens. */
export const SERIES = [
	{ key: "dev", color: "var(--color-chart-cat-1)", dash: undefined },
	{ key: "audit", color: "var(--color-chart-cat-2)", dash: "6 4" },
	{ key: "sealed", color: "var(--color-chart-cat-3)", dash: undefined },
] as const;

export type SeriesKey = (typeof SERIES)[number]["key"];

/** Status pill tone of a run. */
export function statusTone(s: ImprovementStatus): StatusTone {
	switch (s) {
		case "done":
			return "ok";
		case "running":
			return "running";
		case "awaiting_approval":
		case "interrupted":
			return "warn";
		case "failed":
			return "bad";
		default:
			return "neutral";
	}
}

/** Tone of a verdict state; undefined while the run has none. */
export function verdictTone(v: ImprovementVerdictState | undefined): StatusTone {
	switch (v) {
		case "supported":
			return "ok";
		case "not_supported":
			return "bad";
		case "inconclusive":
			return "warn";
		default:
			return "neutral";
	}
}

/** Tone of a lineage node. */
export function nodeTone(s: LineageStatus): StatusTone {
	switch (s) {
		case "committed":
		case "base":
			return "ok";
		case "tripped":
			return "bad";
		case "pending":
			return "running";
		default:
			return "neutral";
	}
}

/** 0.42 as "42%"; undefined as an en dash replacement "-". */
export function pct(v: number | undefined | null): string {
	if (v === undefined || v === null || !Number.isFinite(v)) return "-";
	return `${Math.round(v * 100)}%`;
}

/** A signed point change: 0.05 as "+5.0 pts". */
export function points(v: number | undefined | null): string {
	if (v === undefined || v === null || !Number.isFinite(v)) return "-";
	const p = v * 100;
	return `${p >= 0 ? "+" : ""}${p.toFixed(1)} pts`;
}

/** Dollars with enough precision for small spends. */
export function usd(v: number | undefined | null): string {
	return formatUsd(v);
}

/** The short label of a version ref: agents/x.yaml@v3 as "v3". */
export function versionLabel(ref: string | undefined): string {
	if (!ref) return "-";
	const m = /@v(\d+)$/.exec(ref);
	return m ? `v${m[1]}` : ref;
}

export interface ChartFrame {
	width: number;
	height: number;
	left: number;
	right: number;
	top: number;
	bottom: number;
}

export interface PlacedLine {
	key: SeriesKey;
	path: string;
	points: { x: number; y: number; round: number; value: number }[];
}

export interface CurveModel {
	frame: ChartFrame;
	x: (round: number) => number;
	y: (v: number) => number;
	lines: PlacedLine[];
	band?: { y1: number; y2: number; x1: number; x2: number };
	nullY?: number;
	markers: { x: number; event: LineageStatus; round: number }[];
	rounds: number[];
}

/** Places the curve on a 0 to 100% axis; rounds are evenly spaced. Missing values break no line: they are skipped. */
export function curveModel(curve: readonly CurvePoint[], band: ControlBand | undefined, totalRounds: number): CurveModel {
	const frame: ChartFrame = { width: 640, height: 240, left: 44, right: 16, top: 12, bottom: 32 };
	const last = Math.max(totalRounds, ...curve.map((p) => p.round), 1);
	const plotW = frame.width - frame.left - frame.right;
	const plotH = frame.height - frame.top - frame.bottom;
	const x = (r: number) => frame.left + (r / last) * plotW;
	const y = (v: number) => frame.top + (1 - Math.max(0, Math.min(1, v))) * plotH;
	const lines: PlacedLine[] = SERIES.map(({ key }) => {
		const pts = curve
			.filter((p) => typeof p[key] === "number")
			.map((p) => ({ x: x(p.round), y: y(p[key] as number), round: p.round, value: p[key] as number }));
		const path = pts.map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ");
		return { key, path, points: pts };
	});
	const m: CurveModel = {
		frame,
		x,
		y,
		lines,
		markers: curve.filter((p) => p.round > 0).map((p) => ({ x: x(p.round), event: p.event, round: p.round })),
		rounds: Array.from({ length: last + 1 }, (_, i) => i),
	};
	if (band && band.trials > 0) {
		m.band = { x1: frame.left, x2: frame.width - frame.right, y1: y(band.high), y2: y(band.low) };
	}
	if (band?.null_mean !== undefined) m.nullY = y(band.null_mean);
	return m;
}

export interface TreeRow {
	node: LineageNode;
	depth: number;
}

/** Orders lineage nodes as a tree (children under their parent, oldest first) with their depth. */
export function lineageRows(nodes: readonly LineageNode[]): TreeRow[] {
	const byParent = new Map<string, LineageNode[]>();
	const refs = new Set(nodes.map((n) => n.ref));
	const roots: LineageNode[] = [];
	for (const n of nodes) {
		if (n.parent && refs.has(n.parent) && n.parent !== n.ref) {
			const l = byParent.get(n.parent) ?? [];
			l.push(n);
			byParent.set(n.parent, l);
		} else {
			roots.push(n);
		}
	}
	const out: TreeRow[] = [];
	const seen = new Set<string>();
	const walk = (n: LineageNode, depth: number) => {
		if (seen.has(n.ref)) return;
		seen.add(n.ref);
		out.push({ node: n, depth });
		for (const c of (byParent.get(n.ref) ?? []).sort((a, b) => a.seq - b.seq)) walk(c, depth + 1);
	};
	for (const r of roots.sort((a, b) => a.seq - b.seq)) walk(r, 0);
	return out;
}
/** The verdict states a run of this wave ends in, in the order the list explains them (A2.5). */
export const VERDICT_STATES = [
	"supported",
	"likely_better",
	"insufficient_sample",
	"no_evidence",
	"worse",
	"no_change",
	"not_claimable",
] as const satisfies readonly ImprovementVerdictState[];

/** Tone of a verdict state of this wave; older states keep verdictTone. */
export function stateTone(v: ImprovementVerdictState | undefined): StatusTone {
	switch (v) {
		case "supported":
			return "ok";
		case "likely_better":
		case "insufficient_sample":
			return "warn";
		case "worse":
		case "not_supported":
			return "bad";
		default:
			return verdictTone(v);
	}
}

/** The list badge key (w45imp badge.*) of a state; undefined for older states, which keep the gain pill. */
export function badgeKey(v: ImprovementVerdictState | undefined): (typeof VERDICT_STATES)[number] | undefined {
	return VERDICT_STATES.find((s) => s === v);
}

/** Unsigned points with the A2.12 rule: no decimals at 10 or more, one decimal below. 23.94 as "24", 4.25 as "4.3". */
export function pointsValue(p: number): string {
	if (!Number.isFinite(p)) return "-";
	return Math.abs(p) >= 10 ? String(Math.round(p)) : p.toFixed(1);
}

/** Money with two decimals and no currency sign (the texts carry the "$"). */
export function money(v: number | undefined | null): string {
	return v === undefined || v === null || !Number.isFinite(v) ? "-" : v.toFixed(2);
}

/** Whole minutes. */
export function minutes(v: number): string {
	return Number.isFinite(v) ? String(Math.round(v)) : "-";
}

/**
 * The smallest gain a held-out set can confirm, in points, or undefined when it cannot be estimated:
 * the server stores 0 when there were fewer than 2 tasks, which must never read as "0".
 */
export function detectablePoints(mdeFraction: number | undefined, n: number | undefined): string | undefined {
	if (mdeFraction === undefined || !Number.isFinite(mdeFraction) || mdeFraction <= 0 || (n !== undefined && n < 2)) return undefined;
	return pointsValue(mdeFraction * 100);
}

/** The version number of a ref (agents/x.yaml@v3 as 3), or undefined. */
export function versionNumber(ref: string | undefined): number | undefined {
	const m = ref ? /@v(\d+)$/.exec(ref) : null;
	return m ? Number(m[1]) : undefined;
}

/** The agent id of agents/<id>.yaml or agents/<id>.yaml@v<N>. */
export function agentIdOf(ref: string): string {
	const m = /^agents\/([^/@]+)\.ya?ml(@v\d+)?$/.exec(ref);
	return m ? m[1] : ref;
}