import { getLocale } from "@/lib/i18n";
import type { Pareto } from "@/lib/types/bench";
import { useId, useMemo } from "react";
import { useTranslation } from "react-i18next";
import { costText, formatShare, formatUsd, observed, paretoModel, type PlacedParetoPoint } from "./pareto.utils";

export interface ParetoChartProps {
	pareto: Pareto;
	testId?: string;
}

/**
 * Cost against verified success, hand-drawn in SVG like the trends chart (no chart dependency).
 * Each config is a point with its success interval; the frontier is joined by a line and its points
 * are filled; dominated points are hollow and grey. Status is never color alone: the list under the
 * chart says in words which points are on the frontier, which dominate the rest, and which could
 * not be placed (unknown cost or success is never drawn at zero).
 */
export function ParetoChart({ pareto, testId = "pareto-chart" }: ParetoChartProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const titleId = useId();
	const descId = useId();
	const model = useMemo(() => paretoModel(pareto), [pareto]);
	const { frame } = model;
	const right = frame.width - frame.right;
	const bottom = frame.height - frame.bottom;
	const frontier = pareto.points.filter((p) => p.frontier).map((p) => p.label || p.id);

	return (
		<figure className="flex min-w-0 flex-col gap-3" data-testid={testId}>
			<div className="min-w-0 overflow-x-auto">
				<svg
					role="img"
					aria-labelledby={`${titleId} ${descId}`}
					viewBox={`0 0 ${frame.width} ${frame.height}`}
					// M4-27: never drawn narrower than its 640-unit frame, so the 11 px labels stay 11 px; a phone swipes it.
					className="h-auto w-full max-w-3xl min-w-[640px]"
					data-testid={`${testId}-svg`}
				>
					<title id={titleId}>
						{t("migration.pareto.summary", { axis: pareto.x_label, placed: model.placed.length, count: pareto.points.length })}
					</title>
					<desc id={descId}>
						{frontier.length > 0 ? t("migration.pareto.frontierList", { list: frontier.join(", ") }) : t("migration.pareto.noFrontier")}
					</desc>
					<g aria-hidden="true" className="text-muted-foreground" fontSize={11}>
						{model.yTicks.map((tick) => (
							<g key={`y-${tick.value}`}>
								<line x1={frame.left} x2={right} y1={tick.pos} y2={tick.pos} stroke="var(--color-chart-track)" strokeWidth={1} />
								<text x={frame.left - 8} y={tick.pos} dy="0.32em" textAnchor="end" fill="currentColor">
									{formatShare(tick.value, locale)}
								</text>
							</g>
						))}
						<line x1={frame.left} x2={right} y1={bottom} y2={bottom} stroke="currentColor" strokeOpacity={0.4} strokeWidth={1} />
						{model.xTicks.map((tick, i) => (
							<text
								key={`x-${tick.value}`}
								x={tick.pos}
								y={bottom + 18}
								textAnchor={i === 0 ? "start" : i === model.xTicks.length - 1 ? "end" : "middle"}
								fill="currentColor"
							>
								{formatUsd(tick.value, locale)}
							</text>
						))}
						<text x={(frame.left + right) / 2} y={frame.height - 6} textAnchor="middle" fill="currentColor">
							{pareto.x_label}
						</text>
					</g>
					{model.frontierPath && (
						<path
							d={model.frontierPath}
							fill="none"
							stroke="var(--color-chart-cat-1)"
							strokeWidth={2}
							strokeDasharray="6 4"
							data-testid={`${testId}-frontier`}
						/>
					)}
					{model.placed.map((p) => (
						<Point key={p.point.id} placed={p} locale={locale} testId={testId} />
					))}
				</svg>
			</div>
			<figcaption className="text-muted-foreground flex flex-col gap-2 text-xs" data-testid={`${testId}-legend`}>
				<ul className="flex flex-wrap gap-x-4 gap-y-1">
					<li className="flex items-center gap-1.5">
						<svg width={14} height={14} aria-hidden="true">
							<circle cx={7} cy={7} r={5} fill="var(--color-chart-cat-1)" />
						</svg>
						{t("migration.pareto.legendFrontier")}
					</li>
					<li className="flex items-center gap-1.5">
						<svg width={14} height={14} aria-hidden="true">
							<circle cx={7} cy={7} r={4.5} fill="none" stroke="var(--color-chart-neutral)" strokeWidth={1.5} />
						</svg>
						{t("migration.pareto.legendDominated")}
					</li>
					<li className="flex items-center gap-1.5">
						<svg width={14} height={14} aria-hidden="true">
							<line x1={7} x2={7} y1={2} y2={12} stroke="currentColor" strokeWidth={1.5} />
						</svg>
						{t("migration.pareto.legendInterval")}
					</li>
				</ul>
				<ul className="flex flex-col gap-0.5" data-testid={`${testId}-points`}>
					{pareto.points.map((p) => (
						<li
							key={p.id}
							className="text-foreground"
							data-testid={`${testId}-point-${p.id}`}
							data-frontier={p.frontier ? "true" : undefined}
						>
							<span className="font-mono">{p.label || p.id}</span>{" "}
							{!p.placed
								? t("migration.pareto.notPlaced", { reason: p.reason })
								: p.frontier
									? t("migration.pareto.onFrontier", { x: costText(p.x, locale) ?? "", y: formatShare(observed(p.y), locale) ?? "" })
									: t("migration.pareto.dominated", {
											x: costText(p.x, locale) ?? "",
											y: formatShare(observed(p.y), locale) ?? "",
											by: p.dominated_by.join(", "),
										})}
						</li>
					))}
				</ul>
				<p>{pareto.note}</p>
			</figcaption>
		</figure>
	);
}

function Point({ placed, locale, testId }: { placed: PlacedParetoPoint; locale: string; testId: string }) {
	const { point, x, y, interval } = placed;
	const color = point.frontier ? "var(--color-chart-cat-1)" : "var(--color-chart-neutral)";
	const tip = `${point.label || point.id}: ${costText(point.x, locale) ?? ""}, ${formatShare(observed(point.y), locale) ?? ""}`;
	return (
		<g data-testid={`${testId}-mark`} data-frontier={point.frontier ? "true" : "false"} opacity={point.frontier ? 1 : 0.7}>
			<title>{tip}</title>
			{interval && (
				<g stroke={color} strokeWidth={1.5} strokeOpacity={0.6} data-testid={`${testId}-interval`}>
					<line x1={x} x2={x} y1={interval[0]} y2={interval[1]} />
					<line x1={x - 4} x2={x + 4} y1={interval[0]} y2={interval[0]} />
					<line x1={x - 4} x2={x + 4} y1={interval[1]} y2={interval[1]} />
				</g>
			)}
			{point.frontier ? (
				<circle cx={x} cy={y} r={5} fill={color} stroke="var(--color-background)" strokeWidth={2} />
			) : (
				<circle cx={x} cy={y} r={4.5} fill="var(--color-background)" stroke={color} strokeWidth={1.5} />
			)}
			<text x={x + 8} y={y - 8} fontSize={11} fill="currentColor" className="text-foreground">
				{point.label || point.id}
			</text>
			<circle cx={x} cy={y} r={12} fill="transparent" />
		</g>
	);
}