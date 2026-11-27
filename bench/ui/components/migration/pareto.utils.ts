import type { CostPart, Pareto, ParetoPoint, Value } from "@/lib/types/bench";

/** Plot frame of the Pareto chart in SVG units. */
export interface ParetoFrame {
	width: number;
	height: number;
	left: number;
	right: number;
	top: number;
	bottom: number;
}

export const PARETO_FRAME: ParetoFrame = { width: 640, height: 320, left: 64, right: 24, top: 16, bottom: 48 };

/** One placed point in SVG units, with its success interval when it has one. */
export interface PlacedParetoPoint {
	point: ParetoPoint;
	x: number;
	y: number;
	interval: [number, number] | null;
}

export interface ParetoModel {
	frame: ParetoFrame;
	placed: PlacedParetoPoint[];
	unplaced: ParetoPoint[];
	/** Polyline through the frontier points, cheapest first; "" with fewer than two. */
	frontierPath: string;
	xTicks: { value: number; pos: number }[];
	yTicks: { value: number; pos: number }[];
}

/** The observed number of a value, or null. */
export function observed(v: Value | null | undefined): number | null {
	return v && v.state === "observed" && typeof v.v === "number" && Number.isFinite(v.v) ? v.v : null;
}

/** A rounded axis maximum: 1, 2 or 5 times a power of ten at or above max. */
export function niceMax(max: number): number {
	if (!(max > 0)) return 1;
	const p = 10 ** Math.floor(Math.log10(max));
	for (const m of [1, 2, 5, 10]) {
		if (m * p >= max) return m * p;
	}
	return 10 * p;
}

/**
 * Places the plane's points: x from 0 to a rounded maximum cost, y (verified success) from 0 to 1.
 * Points the server did not place (cost or success unknown) are kept apart for the list under the
 * chart; nothing unknown is drawn at zero.
 */
export function paretoModel(pareto: Pareto, frame: ParetoFrame = PARETO_FRAME): ParetoModel {
	const placedPoints = pareto.points.filter((p) => p.placed && observed(p.x) !== null && observed(p.y) !== null);
	const unplaced = pareto.points.filter((p) => !placedPoints.includes(p));
	const xMax = niceMax(Math.max(0, ...placedPoints.map((p) => observed(p.x) ?? 0)) * 1.05);
	const plotW = frame.width - frame.left - frame.right;
	const plotH = frame.height - frame.top - frame.bottom;
	const xOf = (v: number) => frame.left + (Math.min(Math.max(v, 0), xMax) / xMax) * plotW;
	const yOf = (v: number) => frame.top + plotH - Math.min(Math.max(v, 0), 1) * plotH;
	const placed = placedPoints.map((point): PlacedParetoPoint => {
		const b = point.y.bounds;
		return { point, x: xOf(observed(point.x) ?? 0), y: yOf(observed(point.y) ?? 0), interval: b ? [yOf(b[0]), yOf(b[1])] : null };
	});
	const frontier = placed.filter((p) => p.point.frontier).sort((a, b) => a.x - b.x || b.y - a.y);
	const frontierPath = frontier.length >= 2 ? frontier.map((p, i) => `${i === 0 ? "M" : "L"}${round(p.x)},${round(p.y)}`).join(" ") : "";
	const xTicks = [0, 0.25, 0.5, 0.75, 1].map((f) => ({ value: f * xMax, pos: xOf(f * xMax) }));
	const yTicks = [0, 0.25, 0.5, 0.75, 1].map((v) => ({ value: v, pos: yOf(v) }));
	return { frame, placed, unplaced, frontierPath, xTicks, yTicks };
}

function round(x: number): number {
	return Math.round(x * 10) / 10;
}

/**
 * US dollars, the rule of the Result and Cost tabs (Q-137): 2 decimals at or above $0.01 ($0.10, $1.25), 4 under a
 * cent so a small cost stays readable ($0.0041).
 */
export function formatUsd(x: number, locale = "en"): string {
	const abs = Math.abs(x);
	const digits = abs === 0 ? 0 : abs < 0.01 ? 4 : 2;
	const text = new Intl.NumberFormat(locale, {
		style: "currency",
		currency: "USD",
		minimumFractionDigits: digits,
		maximumFractionDigits: digits,
	}).format(abs);
	return x < 0 ? `-${text}` : text;
}

/** A cost value as text: the amount, a range for an unknown with bounds, else null (the caller says unknown). */
export function costText(v: Value | null | undefined, locale = "en"): string | null {
	const x = observed(v);
	if (x !== null) return formatUsd(x, locale);
	if (v?.bounds) return `${formatUsd(v.bounds[0], locale)} to ${formatUsd(v.bounds[1], locale)}`;
	return null;
}

/** A signed part: "+$0.012", "-$0.004", "$0". */
export function signedUsd(x: number, locale = "en"): string {
	return x > 0 ? `+${formatUsd(x, locale)}` : formatUsd(x, locale);
}

/** A share of 1 as a percentage with one decimal. */
export function formatShare(x: number | null, locale = "en"): string | null {
	if (x === null || !Number.isFinite(x)) return null;
	return new Intl.NumberFormat(locale, { style: "percent", minimumFractionDigits: 1, maximumFractionDigits: 1 }).format(x);
}

/** One waterfall bar: where it starts and ends, as fractions of the widest running total. */
export interface WaterfallBar {
	part: CostPart;
	start: number;
	end: number;
	known: boolean;
}

/**
 * The bars of a waterfall in order. Each observed part moves the running total from start to end;
 * an unknown part is drawn as its bounds' range from the running total, flagged unknown. Fractions
 * are of the largest absolute running position, so the widest bar spans the track.
 */
export function waterfallBars(parts: readonly CostPart[]): WaterfallBar[] {
	let running = 0;
	const raw = parts.map((part) => {
		const x = observed(part.usd);
		if (x !== null) {
			const bar = { part, start: running, end: running + x, known: true };
			running += x;
			return bar;
		}
		const b = part.usd.bounds;
		return { part, start: running + (b ? b[0] : 0), end: running + (b ? b[1] : 0), known: false };
	});
	const scale = Math.max(1e-12, ...raw.flatMap((b) => [Math.abs(b.start), Math.abs(b.end)]));
	return raw.map((b) => ({ ...b, start: b.start / scale, end: b.end / scale }));
}