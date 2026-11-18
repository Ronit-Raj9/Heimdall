import { clipText } from "@/components/runs/stepTimeline.utils";
import { getLocale } from "@/lib/i18n";
import type { AgentHandoff, AgentSpan, AgentStats } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronUp } from "lucide-react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { AgentId, RoleChip, UnknownValue } from "./agentBadges";
import {
	factState,
	formatDurationMs,
	formatOneDecimal,
	formatPercent,
	handoffMarkers,
	kindColor,
	knownEdgeKind,
	knownSpanKind,
	laneLayout,
	parallelism,
	presentKinds,
	shareOfWall,
	type HandoffMarker,
} from "./agents.utils";

export interface AgentSwimlaneProps {
	spans: readonly AgentSpan[];
	members: readonly AgentStats[];
	wallMs: number;
	busyMs: number;
	criticalMs: number;
	/** Drawn as markers between the sender's and the receiver's lanes at at_ms. */
	handoffs?: readonly AgentHandoff[];
	arm: string;
}

const TICKS = [0, 25, 50, 75, 100];

/** Height of one lane row in rem; the handoff overlay places its markers on the same grid. */
const LANE_REM = 2;

type TFn = ReturnType<typeof useTranslation<"bench">>["t"];

function kindLabel(t: TFn, kind: string): string {
	const known = knownSpanKind(kind);
	return known ? t(`runs.agents.kind.${known}`) : clipText(kind, 32);
}

/** A handoff that arrived altered or lost a planted fact. */
function handoffWarn(handoff: AgentHandoff): boolean {
	return Boolean(handoff.altered) || (handoff.facts ?? []).some((fact) => factState(fact) === "lost");
}

/**
 * One lane per agent, in tree order, with each timeline row drawn as a block over the run's wall
 * time: coloured by kind, errors marked, blocks on the critical path ringed and the rest dimmed.
 * Above the lanes, wall time, the agents' summed busy time, the parallelism factor (busy over
 * wall) and the critical path. Each handoff with a known time is a vertical marker from the
 * sender's lane to the receiver's, amber when the payload arrived altered or lost a fact. Block
 * labels and agent ids are agent text: they reach titles and accessible names as plain strings,
 * never HTML and never through a translation.
 */
export function AgentSwimlane({ spans, members, wallMs, busyMs, criticalMs, handoffs = [], arm }: AgentSwimlaneProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const roles = useMemo(() => new Map(members.map((m) => [m.agent_id, m.role])), [members]);
	const lanes = useMemo(
		() =>
			laneLayout(
				spans,
				members.map((m) => m.agent_id),
				wallMs,
			),
		[spans, members, wallMs],
	);
	const kinds = useMemo(() => presentKinds(spans), [spans]);
	const markers = useMemo(
		() =>
			handoffMarkers(
				handoffs,
				lanes.map((lane) => lane.agentId),
				wallMs,
			),
		[handoffs, lanes, wallMs],
	);
	const factor = parallelism(busyMs, wallMs);
	const criticalShare = shareOfWall(criticalMs, wallMs);
	const anyCritical = spans.some((span) => span.critical);
	const anyError = spans.some((span) => span.is_error);
	const anyWarnMarker = markers.some(({ handoff }) => handoffWarn(handoff));

	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`agents-swimlane-${arm}`}>
			<dl
				className="bg-border grid grid-cols-2 gap-px overflow-hidden rounded-lg border sm:grid-cols-4"
				data-testid={`agents-stats-${arm}`}
			>
				<Stat label={t("runs.agents.stats.wall")} hint={t("runs.agents.stats.wallHint")} testId={`agents-stat-wall-${arm}`}>
					{formatDurationMs(wallMs, locale)}
				</Stat>
				<Stat label={t("runs.agents.stats.busy")} hint={t("runs.agents.stats.busyHint")} testId={`agents-stat-busy-${arm}`}>
					{formatDurationMs(busyMs, locale)}
				</Stat>
				<Stat
					label={t("runs.agents.stats.parallelism")}
					hint={t("runs.agents.stats.parallelismHint")}
					testId={`agents-stat-parallelism-${arm}`}
				>
					{factor === null ? (
						<UnknownValue hint={t("runs.agents.stats.noWall")} />
					) : (
						t("runs.agents.stats.factor", { value: formatOneDecimal(factor, locale) })
					)}
				</Stat>
				<Stat label={t("runs.agents.stats.critical")} hint={t("runs.agents.stats.criticalHint")} testId={`agents-stat-critical-${arm}`}>
					{formatDurationMs(criticalMs, locale)}
					{criticalShare !== null && (
						<span className="text-muted-foreground ml-1.5 text-xs font-normal">
							{t("runs.agents.stats.ofWall", { percent: formatPercent(criticalShare, locale) })}
						</span>
					)}
				</Stat>
			</dl>

			{spans.length === 0 ? (
				<p
					className="text-muted-foreground rounded-md border border-dashed px-3 py-4 text-center text-xs"
					data-testid={`agents-swimlane-${arm}-empty`}
				>
					{t("runs.agents.swimlane.empty")}
				</p>
			) : (
				<>
					<ul
						className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]"
						aria-label={t("runs.agents.swimlane.legend")}
						data-testid={`agents-legend-${arm}`}
					>
						{kinds.map((kind) => (
							<li key={kind} className="inline-flex items-center gap-1.5">
								<span aria-hidden="true" className="h-2.5 w-4 rounded-[3px]" style={{ background: kindColor(kind) }} />
								{kindLabel(t, kind)}
							</li>
						))}
						{anyError && (
							<li className="inline-flex items-center gap-1.5">
								<span aria-hidden="true" className="border-chart-error bg-chart-error/20 h-2.5 w-4 rounded-[3px] border-2" />
								{t("runs.agents.swimlane.error")}
							</li>
						)}
						{anyCritical && (
							<li className="inline-flex items-center gap-1.5">
								<span
									aria-hidden="true"
									className="ring-foreground ring-offset-card bg-muted-foreground/40 h-2.5 w-4 rounded-[3px] ring-2 ring-offset-1"
								/>
								{t("runs.agents.swimlane.critical")}
							</li>
						)}
						{markers.length > 0 && (
							<li className="inline-flex items-center gap-1.5" data-testid={`agents-legend-${arm}-handoff`}>
								<span aria-hidden="true" className="bg-foreground/70 h-3 w-0.5 rounded-full" />
								{t("runs.agents.swimlane.handoff")}
							</li>
						)}
						{anyWarnMarker && (
							<li className="inline-flex items-center gap-1.5" data-testid={`agents-legend-${arm}-altered`}>
								<span aria-hidden="true" className="bg-chart-warning h-3 w-0.5 rounded-full" />
								{t("runs.agents.swimlane.alteredHandoff")}
							</li>
						)}
					</ul>

					<div className="bg-card min-w-0 overflow-x-auto rounded-lg border shadow-(--shadow-card)">
						<div className="relative min-w-[36rem] py-2">
							<div className="flex flex-col" role="list" aria-label={t("runs.agents.swimlane.label", { arm })}>
								{lanes.map((lane) => (
									<div
										key={lane.agentId}
										role="listitem"
										className="grid h-8 grid-cols-[minmax(8rem,11rem)_1fr] items-center gap-3 px-3"
										data-testid={`agents-lane-${arm}-${lane.agentId}`}
										data-critical={lane.critical}
									>
										<div className="flex min-w-0 items-center gap-1.5 text-xs">
											<span
												aria-hidden="true"
												className={cn("size-1.5 shrink-0 rounded-full", lane.critical ? "bg-foreground" : "bg-transparent")}
											/>
											<AgentId id={lane.agentId} className="truncate text-[11px] font-medium" />
											<RoleChip role={roles.get(lane.agentId)} />
										</div>
										<div
											className="bg-chart-track/70 relative h-6 rounded-[4px]"
											role="group"
											aria-label={`${t("runs.agents.swimlane.lane")} ${clipText(lane.agentId, 48)}`}
										>
											{/* Quarter guides, so block positions read against the axis below. */}
											{TICKS.slice(1, -1).map((tick) => (
												<span key={tick} aria-hidden="true" className="bg-border absolute inset-y-0 w-px" style={{ left: `${tick}%` }} />
											))}
											{lane.blocks.map(({ span, left, width }) => {
												// Kind and label are joined outside t(), so no row text is ever interpolated by it.
												const aria = `${kindLabel(t, span.kind)}, ${t("runs.agents.swimlane.block", {
													seq: span.seq,
													start: formatDurationMs(span.start_ms, locale),
													end: formatDurationMs(span.end_ms, locale),
												})}${span.critical ? `, ${t("runs.agents.swimlane.critical")}` : ""}${span.is_error ? `, ${t("runs.agents.swimlane.error")}` : ""}${span.label ? `: ${clipText(span.label, 160)}` : ""}`;
												return (
													<span
														key={`${span.seq}-${span.start_ms}`}
														role="img"
														aria-label={aria}
														title={aria}
														className={cn(
															"absolute top-1 bottom-1 min-w-[3px] rounded-[3px] transition-opacity",
															span.critical
																? "ring-foreground ring-offset-card z-10 opacity-100 ring-2 ring-offset-1"
																: "opacity-70 hover:opacity-100",
															span.is_error && "border-chart-error border-2",
														)}
														style={{ left: `${left}%`, width: `${width}%`, background: kindColor(span.kind) }}
														data-testid={`agents-block-${arm}-${span.seq}`}
														data-kind={span.kind}
														data-critical={Boolean(span.critical)}
														data-error={Boolean(span.is_error)}
													>
														{span.is_error && (
															<span
																aria-hidden="true"
																className="bg-chart-error ring-card absolute -top-1.5 left-1/2 size-2 -translate-x-1/2 rounded-full ring-2"
															/>
														)}
													</span>
												);
											})}
										</div>
									</div>
								))}
							</div>
							{markers.length > 0 && (
								// Same column grid as the lanes, so a marker's percent lands on the lane track.
								<div
									className="pointer-events-none absolute inset-x-0 top-2 grid grid-cols-[minmax(8rem,11rem)_1fr] gap-3 px-3"
									style={{ height: `${lanes.length * LANE_REM}rem` }}
									role="group"
									aria-label={t("runs.agents.swimlane.handoffs")}
									data-testid={`agents-handoff-markers-${arm}`}
								>
									<span />
									<div className="relative">
										{markers.map((marker) => (
											<HandoffMarkerView key={marker.handoff.index} marker={marker} arm={arm} />
										))}
									</div>
								</div>
							)}
							<div className="grid grid-cols-[minmax(8rem,11rem)_1fr] gap-3 px-3 pt-1" aria-hidden="true">
								<span />
								<div className="text-muted-foreground relative h-4 font-mono text-[10px] tabular-nums">
									{TICKS.map((tick) => (
										<span
											key={tick}
											className={cn(
												"absolute top-0 whitespace-nowrap",
												tick === 0 ? "" : tick === 100 ? "-translate-x-full" : "-translate-x-1/2",
											)}
											style={{ left: `${tick}%` }}
										>
											{formatDurationMs((wallMs * tick) / 100, locale)}
										</span>
									))}
								</div>
							</div>
						</div>
					</div>
				</>
			)}
		</div>
	);
}

/**
 * One handoff as a vertical connector at its time: a dot on the sender's lane, a chevron on the
 * receiver's. A handoff within one lane is a short tick. Ids are joined into the accessible name
 * outside t(), so no agent text is interpolated by a translation.
 */
function HandoffMarkerView({ marker, arm }: { marker: HandoffMarker; arm: string }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const { handoff, left, fromLane, toLane } = marker;
	const kind = knownEdgeKind(handoff.kind);
	const warn = handoffWarn(handoff);
	const same = toLane === fromLane;
	const down = toLane > fromLane;
	const top = same ? fromLane * LANE_REM + 0.25 : Math.min(fromLane, toLane) * LANE_REM + LANE_REM / 2;
	const height = same ? LANE_REM - 0.5 : Math.abs(toLane - fromLane) * LANE_REM;
	const kindText = kind ? t(`runs.agents.edge.${kind}`) : clipText(handoff.kind, 20);
	const time = t("runs.agents.swimlane.handoffAt", { time: formatDurationMs(handoff.at_ms ?? 0, locale) });
	const aria = `${kindText} #${handoff.index}: ${clipText(handoff.from, 48)} ${t("runs.agents.handoffs.to")} ${clipText(handoff.to, 48)}, ${time}${
		warn ? `, ${t("runs.agents.handoffs.altered")}` : ""
	}`;
	const Chevron = down ? ChevronDown : ChevronUp;
	return (
		<span
			role="img"
			aria-label={aria}
			title={aria}
			className={cn("pointer-events-auto absolute z-20 w-3 -translate-x-1/2", warn ? "text-chart-warning" : "text-foreground/70")}
			style={{ left: `${left}%`, top: `${top}rem`, height: `${height}rem` }}
			data-testid={`agents-handoff-marker-${arm}-${handoff.index}`}
			data-direction={same ? "same" : down ? "down" : "up"}
			data-warn={warn}
		>
			<span aria-hidden="true" className="absolute inset-y-0 left-1/2 w-0.5 -translate-x-1/2 rounded-full bg-current" />
			{!same && (
				<>
					<span
						aria-hidden="true"
						className={cn(
							"ring-card absolute left-1/2 size-1.5 -translate-x-1/2 rounded-full bg-current ring-2",
							down ? "-top-[3px]" : "-bottom-[3px]",
						)}
					/>
					<Chevron
						aria-hidden="true"
						strokeWidth={3}
						className={cn("absolute left-1/2 size-3 -translate-x-1/2", down ? "-bottom-1.5" : "-top-1.5")}
					/>
				</>
			)}
		</span>
	);
}

function Stat({ label, hint, children, testId }: { label: string; hint: string; children: React.ReactNode; testId: string }) {
	return (
		<div className="bg-card flex min-w-0 flex-col gap-0.5 px-3 py-2" data-testid={testId}>
			<dt className="text-muted-foreground text-[11px] font-medium tracking-[0.01em]" title={hint}>
				{label}
			</dt>
			<dd className="text-foreground text-sm font-semibold tabular-nums">{children}</dd>
		</div>
	);
}