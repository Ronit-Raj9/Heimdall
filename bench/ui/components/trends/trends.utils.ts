import { formatValueNumber } from "@/components/valueCell.utils";
import type { TrendLineage, TrendMetric, TrendPoint, TrendSynthetic, Trends, ValueKind } from "@/lib/types/bench";

/** Chartable metrics in display order (trends.Metrics). */
export const TREND_METRICS = [
	"verified_success",
	"cost_per_success",
	"tokens",
	"wall_time_s",
	"false_completion",
] as const satisfies readonly TrendMetric[];

/** Synthetic filter values. */
export const TREND_SYNTHETIC = ["exclude", "include", "only"] as const satisfies readonly TrendSynthetic[];

/** Fewer points than this and the page shows the empty state instead of a chart. */
export const MIN_TREND_POINTS = 2;

/** The picker value for "no filter". Not a valid suite or agent id, so it never collides. */
export const ALL_VALUE = "~all";

/** How each metric's numbers are formatted: rates as percent, cost in dollars, wall time as a duration. */
export const METRIC_KIND: Record<TrendMetric, ValueKind> = {
	verified_success: "rate",
	cost_per_success: "usd",
	tokens: "tokens",
	wall_time_s: "duration",
	false_completion: "rate",
};

/** Number of categorical chart slots (--color-chart-cat-1 to 6); later lineages fold into "other". */
export const SERIES_SLOTS = 6;

/** Formats one metric number for the locale; null when it was not observed, so the caller says so and never shows zero. */
export function formatMetric(value: number | null | undefined, metric: TrendMetric, locale: string): string | null {
	if (typeof value !== "number" || !Number.isFinite(value)) return null;
	return formatValueNumber(value, METRIC_KIND[metric], locale);
}

/** The first 12 characters of a config hash, without a "sha256:" prefix. */
export function shortHash(hash: string): string {
	return hash.replace(/^sha256:/, "").slice(0, 12);
}

/** A lineage's display name: agent, plus the model when known. */
export function lineageLabel(lineage: Pick<TrendLineage, "agent" | "model">): string {
	return lineage.model ? `${lineage.agent} (${lineage.model})` : lineage.agent;
}

/**
 * The categorical slot (0-based) of every lineage, keyed by lineage key. The slot follows the
 * agent's position in the unfiltered agent list, so filtering to one agent never repaints it;
 * a second model of the same agent takes the next free slot. Slots at or past SERIES_SLOTS are
 * drawn in the "other" color with a dashed line, never a generated hue.
 */
export function seriesSlots(lineages: readonly TrendLineage[], agents: readonly string[]): Map<string, number> {
	const out = new Map<string, number>();
	const taken = new Set<number>();
	const order = [...lineages].sort((a, b) => agentRank(a.agent, agents) - agentRank(b.agent, agents) || a.key.localeCompare(b.key));
	for (const lineage of order) {
		let slot = agentRank(lineage.agent, agents);
		while (taken.has(slot)) slot++;
		taken.add(slot);
		out.set(lineage.key, slot);
	}
	return out;
}

function agentRank(agent: string, agents: readonly string[]): number {
	const i = agents.indexOf(agent);
	return i === -1 ? agents.length : i;
}

/** The CSS color of a slot: a categorical token, or the "other" token past the last slot. */
export function slotColor(slot: number): string {
	return slot < SERIES_SLOTS ? `var(--color-chart-cat-${slot + 1})` : "var(--color-chart-cat-other)";
}

/** Point count over every lineage; the server sends it too, this guards a hand-made payload. */
export function totalPoints(data: Pick<Trends, "points" | "lineages">): number {
	if (typeof data.points === "number") return data.points;
	return data.lineages.reduce((n, l) => n + l.points.length, 0);
}

/** True when the release verdict of the point's experiment was regressed or blocked. */
export function isReleaseFlagged(point: Pick<TrendPoint, "release">): boolean {
	return point.release === "regressed" || point.release === "blocked";
}

/** Plot area geometry: the SVG size and its inner margins, in user units. */
export interface ChartFrame {
	width: number;
	height: number;
	left: number;
	right: number;
	top: number;
	bottom: number;
}

export const DEFAULT_FRAME: ChartFrame = { width: 720, height: 280, left: 64, right: 24, top: 16, bottom: 40 };

/** One placed point of a series; y is null when the metric was not observed (a gap in the line). */
export interface PlacedPoint {
	point: TrendPoint;
	x: number;
	y: number | null;
	/** The success interval in pixels, when the chart shows verified success and the interval exists. */
	interval: [number, number] | null;
}

/** One lineage ready to draw. */
export interface PlacedSeries {
	lineage: TrendLineage;
	slot: number;
	points: PlacedPoint[];
	/** SVG path data; a null value breaks the line, so missing data is never bridged. */
	path: string;
}

export interface ChartTick {
	value: number;
	pos: number;
}

/** Everything the chart draws, computed without rendering. */
export interface ChartModel {
	frame: ChartFrame;
	series: PlacedSeries[];
	yTicks: ChartTick[];
	xTicks: ChartTick[];
	yMax: number;
}

const DAY_MS = 86_400_000;

/**
 * Lays out the chart: time on x (experiment date), the metric on y from a zero baseline. Rates
 * use a fixed 0 to 1 axis so a drop is never exaggerated by a zoomed scale.
 */
export function chartModel(
	lineages: readonly TrendLineage[],
	metric: TrendMetric,
	slots: Map<string, number>,
	frame: ChartFrame = DEFAULT_FRAME,
): ChartModel {
	const times = lineages.flatMap((l) => l.points.map((p) => Date.parse(p.date))).filter((t) => Number.isFinite(t));
	let xMin = times.length > 0 ? Math.min(...times) : 0;
	let xMax = times.length > 0 ? Math.max(...times) : DAY_MS;
	if (xMax - xMin < DAY_MS) {
		xMin -= DAY_MS / 2;
		xMax += DAY_MS / 2;
	}
	const values = lineages.flatMap((l) => l.points.flatMap((p) => metricNumbers(p, metric)));
	const isRate = METRIC_KIND[metric] === "rate";
	const yMax = isRate ? 1 : niceMax(values.length > 0 ? Math.max(...values) : 1);
	const plotW = frame.width - frame.left - frame.right;
	const plotH = frame.height - frame.top - frame.bottom;
	const xOf = (t: number) => frame.left + ((t - xMin) / (xMax - xMin)) * plotW;
	const yOf = (v: number) => frame.top + plotH - (Math.min(Math.max(v, 0), yMax) / yMax) * plotH;

	const series = lineages.map((lineage) => {
		const points = lineage.points.map((point): PlacedPoint => {
			const v = point.value;
			const y = typeof v === "number" && Number.isFinite(v) ? yOf(v) : null;
			const lo = point.success_low;
			const hi = point.success_high;
			const interval: [number, number] | null =
				metric === "verified_success" && typeof lo === "number" && typeof hi === "number" ? [yOf(lo), yOf(hi)] : null;
			return { point, x: xOf(Date.parse(point.date)), y, interval };
		});
		return { lineage, slot: slots.get(lineage.key) ?? 0, points, path: linePath(points) };
	});

	const yTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => ({ value: f * yMax, pos: yOf(f * yMax) }));
	const xTicks = dateTicks(xMin, xMax).map((t) => ({ value: t, pos: xOf(t) }));
	return { frame, series, yTicks, xTicks, yMax };
}

/** The numbers a point contributes to the y range: its value and, for success, its interval. */
function metricNumbers(point: TrendPoint, metric: TrendMetric): number[] {
	const out: number[] = [];
	if (typeof point.value === "number" && Number.isFinite(point.value)) out.push(point.value);
	if (metric === "verified_success") {
		for (const v of [point.success_low, point.success_high]) if (typeof v === "number" && Number.isFinite(v)) out.push(v);
	}
	return out;
}

/** SVG path through the placed points; a point with no value ends the current segment. */
export function linePath(points: readonly Pick<PlacedPoint, "x" | "y">[]): string {
	let d = "";
	let open = false;
	for (const p of points) {
		if (p.y === null) {
			open = false;
			continue;
		}
		d += `${open ? "L" : "M"}${round(p.x)},${round(p.y)}`;
		open = true;
	}
	return d;
}

/** The smallest of 1, 2, 2.5 or 5 times a power of ten that is at least max (and above 0). */
export function niceMax(max: number): number {
	if (!(max > 0) || !Number.isFinite(max)) return 1;
	const exp = 10 ** Math.floor(Math.log10(max));
	for (const step of [1, 2, 2.5, 5, 10]) {
		if (step * exp >= max) return step * exp;
	}
	return 10 * exp;
}

/** Up to five evenly spaced date ticks from xMin to xMax inclusive. */
function dateTicks(xMin: number, xMax: number): number[] {
	const n = 4;
	return Array.from({ length: n + 1 }, (_, i) => xMin + ((xMax - xMin) * i) / n);
}

function round(v: number): number {
	return Math.round(v * 10) / 10;
}

/** Short date for an axis tick or a table cell, in the viewer's locale. */
export function formatDate(value: number | string, locale: string): string {
	const t = typeof value === "number" ? value : Date.parse(value);
	if (!Number.isFinite(t)) return String(value);
	return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", year: "numeric" }).format(new Date(t));
}

/** The newest point of a lineage with a charted value, or null. */
export function latestValuePoint(lineage: TrendLineage): TrendPoint | null {
	for (let i = lineage.points.length - 1; i >= 0; i--) {
		const p = lineage.points[i];
		if (typeof p.value === "number" && Number.isFinite(p.value)) return p;
	}
	return null;
}