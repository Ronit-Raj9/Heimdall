import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { ToneBadge } from "@/components/suites/suiteBadges";
import { formatCount } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import { useGetRunStepsQuery } from "@/lib/store/apis/runStepsApi";
import type { AgentEventStatus, ObserverStatus, RunSteps, StepRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle, CircleCheck, Eye, EyeOff, Megaphone } from "lucide-react";
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import "@/app/leaderboard/views/uf4bI18n";
import {
	CORROBORATION_TONE,
	EXECUTION_TONE,
	OBSERVER_TONE,
	PROOF_TONE,
	REPORTED_SOURCE,
	clipText,
	countedSteps,
	eventEntries,
	filterSteps,
	groupStepsByTurn,
	knownChannel,
	knownCorroboration,
	knownEventType,
	observerDegraded,
	presentSources,
	safeCount,
	sourceCounts,
	sourceTone,
	stepDepths,
	stepTime,
	type TurnGroup,
} from "./stepTimeline.utils";

/**
 * One run's step timeline, fetched by run id: loading, error and the unavailable state of a run
 * recorded before the timeline existed are all shown, never a blank box.
 */
export function RunStepTimeline({ runId, arm }: { runId: string; arm: string }) {
	const { t } = useTranslation("bench");
	const steps = useGetRunStepsQuery(runId);
	if (steps.isLoading) return <BlockSkeleton rows={5} testId={`step-timeline-${arm}-loading`} />;
	if (steps.error || !steps.data) {
		return (
			<LoadError
				title={t("runs.steps.loadError")}
				error={steps.error}
				onRetry={() => void steps.refetch()}
				testId={`step-timeline-${arm}-error`}
			/>
		);
	}
	return <StepTimeline data={steps.data} arm={arm} />;
}

export interface StepTimelineProps {
	data: RunSteps;
	arm: string;
	/** Sources hidden at first render. */
	initialHidden?: readonly string[];
}

/**
 * The unified agent step timeline, grouped by logical model step: each model turn, then the
 * commands, file changes and tool calls it caused. Every row names its witness (source) and how
 * strong the proof is; requested tool calls say whether a witness saw them run. Rows the agent
 * reported about itself carry a dashed badge with their channel and corroboration, and indent under
 * their reported parent (subagents, handoffs). Agent-influenced strings (summary, path, tool
 * name, conflict, reported type, report id, channel) are React text nodes only: never HTML, never
 * a link, never passed through a translation, and clipped.
 */
export function StepTimeline({ data, arm, initialHidden = [] }: StepTimelineProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set(initialHidden));
	const sources = useMemo(() => presentSources(data.steps), [data.steps]);
	const counts = useMemo(() => sourceCounts(data.steps), [data.steps]);
	const visible = useMemo(() => filterSteps(data.steps, hidden), [data.steps, hidden]);
	const groups = useMemo(() => groupStepsByTurn(visible), [visible]);
	const depths = useMemo(() => stepDepths(data.steps), [data.steps]);

	if (!data.available) {
		return <EmptyNote testId={`step-timeline-${arm}-unavailable`}>{t("runs.steps.unavailable")}</EmptyNote>;
	}

	const toggle = (source: string) =>
		setHidden((prev) => {
			const next = new Set(prev);
			if (next.has(source)) next.delete(source);
			else next.add(source);
			return next;
		});

	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid={`step-timeline-${arm}`}>
			<ObserverStatusLine observers={data.observers} arm={arm} locale={locale} />
			{data.agent_events && <AgentEventsLine status={data.agent_events} arm={arm} locale={locale} />}
			<p className="text-muted-foreground flex flex-wrap gap-x-4 text-xs" data-testid={`step-timeline-${arm}-summary`}>
				<span className="text-foreground font-medium">{t("runs.steps.counted", { count: countedSteps(data.steps) })}</span>
				<span>{t("runs.steps.shown", { shown: formatCount(visible.length, locale), total: formatCount(data.steps.length, locale) })}</span>
			</p>
			{data.truncated && (
				<p className="text-chart-warning-ink text-xs" data-testid={`step-timeline-${arm}-truncated`}>
					{t("runs.steps.truncated", { count: data.steps.length })}
				</p>
			)}
			{(data.skipped_lines ?? 0) > 0 && (
				<p className="text-chart-warning-ink text-xs" data-testid={`step-timeline-${arm}-skipped`}>
					{t("runs.steps.skipped", { count: data.skipped_lines })}
				</p>
			)}
			{sources.length > 0 && (
				<div className="flex flex-wrap items-center gap-1.5" role="group" aria-label={t("runs.steps.filter")}>
					<span className="text-muted-foreground mr-1 text-xs">{t("runs.steps.filter")}</span>
					{sources.map((source) => {
						const on = !hidden.has(source);
						const label = sourceLabel(t, source);
						return (
							<button
								key={source}
								type="button"
								aria-pressed={on}
								aria-label={t("runs.steps.filterAria", { source: label })}
								onClick={() => toggle(source)}
								data-testid={`step-filter-${arm}-${source}`}
								data-on={on}
								className={cn(
									"focus-visible:ring-ring/50 inline-flex h-6 cursor-pointer items-center gap-1 rounded-full border px-2.5 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
									on ? "border-primary bg-primary/10 text-foreground" : "text-muted-foreground hover:bg-accent line-through",
								)}
							>
								{on ? <Eye className="size-3" aria-hidden="true" /> : <EyeOff className="size-3" aria-hidden="true" />}
								{label}
								<span className="tabular-nums">{formatCount(counts.get(source) ?? 0, locale)}</span>
							</button>
						);
					})}
				</div>
			)}
			{data.steps.length === 0 ? (
				<EmptyNote testId={`step-timeline-${arm}-empty`}>{t("runs.steps.empty")}</EmptyNote>
			) : groups.length === 0 ? (
				<EmptyNote testId={`step-timeline-${arm}-filtered`}>{t("runs.steps.noRows")}</EmptyNote>
			) : (
				// A rail down the left joins the turns, so the list reads as one timeline.
				<ol
					className="before:bg-border relative flex min-w-0 flex-col gap-3 pl-5 before:absolute before:top-3 before:bottom-3 before:left-[5px] before:w-px"
					aria-label={t("runs.steps.label", { arm })}
				>
					{groups.map((group) => (
						<TurnBlock key={group.key} group={group} arm={arm} depths={depths} />
					))}
				</ol>
			)}
		</div>
	);
}

type TFn = ReturnType<typeof useTranslation<"bench">>["t"];

function sourceLabel(t: TFn, source: string): string {
	return t(`runs.steps.source.${source}`, { defaultValue: clipText(source, 40) });
}

function TurnBlock({ group, arm, depths }: { group: TurnGroup; arm: string; depths: ReadonlyMap<number, number> }) {
	const { t } = useTranslation("bench");
	return (
		<li
			className="bg-card after:bg-muted-foreground/60 after:ring-card relative min-w-0 rounded-lg border shadow-(--shadow-card) after:absolute after:top-3 after:-left-[19px] after:size-[9px] after:rounded-full after:ring-4"
			data-testid={`step-turn-${arm}-${group.session ?? 0}-${group.turn}`}
			data-turn={group.turn}
		>
			<div className="bg-muted/50 text-muted-foreground flex flex-wrap items-center gap-2 rounded-t-lg border-b px-3 py-2 text-xs">
				<span className="text-foreground font-semibold">
					{group.turn === 0 ? t("runs.steps.beforeFirst") : t("runs.steps.turn", { turn: group.turn })}
				</span>
				{group.session ? <span>{t("runs.steps.session", { session: group.session })}</span> : null}
			</div>
			<ul className="divide-border/70 divide-y">
				{group.model.map((step) => (
					<StepRow key={step.seq} step={step} arm={arm} model depth={depths.get(step.seq) ?? 0} />
				))}
				{group.caused.map((step) => (
					<StepRow key={step.seq} step={step} arm={arm} depth={depths.get(step.seq) ?? 0} />
				))}
			</ul>
		</li>
	);
}

/**
 * The reported event type: a translated label for the known types, else the agent's own value as
 * clipped text (never a translation key, never interpolated).
 */
function ReportedEventType({ value, testId }: { value: string; testId: string }) {
	const { t } = useTranslation("bench");
	const known = knownEventType(value);
	return (
		<span className="text-muted-foreground font-mono" data-testid={testId}>
			{known ? (
				t(`runs.steps.reported.eventType.${known}`)
			) : (
				<>
					<span className="sr-only">{t("runs.steps.reported.type")} </span>
					{clipText(value, 40)}
				</>
			)}
		</span>
	);
}

/**
 * The source badge of an agent-reported row: dashed and marked, so it never reads like a trusted
 * witness or the agent's stream, with the channel the report arrived on.
 */
function ReportedSourceBadge({ channel, testId }: { channel?: string; testId: string }) {
	const { t } = useTranslation("bench");
	const known = knownChannel(channel);
	const channelText = known ? t(`runs.steps.reported.channel.${known}`) : clipText(channel, 24);
	return (
		<ToneBadge
			tone={sourceTone(REPORTED_SOURCE)}
			icon={Megaphone}
			hint={t("runs.steps.sourceHint.agent_reported")}
			testId={testId}
			className="border-dashed"
		>
			{t("runs.steps.source.agent_reported")}
			{channelText && (
				<span className="border-l border-current/30 pl-1 font-mono" data-testid={`${testId}-channel`}>
					{channelText}
				</span>
			)}
		</ToneBadge>
	);
}

/** Whether a trusted witness backs an agent-reported row. An unknown value is shown as neutral text. */
function CorroborationBadge({ value, testId }: { value: string; testId: string }) {
	const { t } = useTranslation("bench");
	const known = knownCorroboration(value);
	if (!known) {
		return (
			<ToneBadge tone="neutral" testId={testId}>
				{clipText(value, 40)}
			</ToneBadge>
		);
	}
	return (
		<ToneBadge
			tone={CORROBORATION_TONE[known]}
			icon={known === "conflicts" ? AlertTriangle : undefined}
			hint={t(`runs.steps.reported.corroborationHint.${known}`)}
			testId={testId}
		>
			{t(`runs.steps.reported.corroboration.${known}`)}
		</ToneBadge>
	);
}

function StepRow({ step, arm, model = false, depth = 0 }: { step: StepRecord; arm: string; model?: boolean; depth?: number }) {
	const { t } = useTranslation("bench");
	const summary = clipText(step.summary);
	const toolName = clipText(step.tool_name, 80);
	const path = clipText(step.path, 160);
	const conflict = clipText(step.conflict);
	const reported = step.source === REPORTED_SOURCE;
	const reportId = clipText(step.report_id, 80);
	return (
		<li
			className={cn(
				"hover:bg-muted/40 flex min-w-0 flex-col gap-1 px-3 py-2 text-xs transition-colors duration-100",
				!model && "pl-6",
				reported && "border-l-chart-warning/60 border-l-2 border-dashed",
				step.is_error && "bg-chart-error/5 shadow-[inset_2px_0_0_var(--chart-error)]",
			)}
			style={depth > 0 ? { marginLeft: `${depth * 1.25}rem` } : undefined}
			data-testid={`step-row-${arm}-${step.seq}`}
			data-kind={step.kind}
			data-source={step.source}
			data-depth={depth}
		>
			<div className="flex min-w-0 flex-wrap items-center gap-1.5">
				<span className="text-muted-foreground w-8 shrink-0 text-right font-mono tabular-nums">#{step.seq}</span>
				<span className="text-muted-foreground font-mono tabular-nums">{stepTime(step.ts)}</span>
				<span className={cn("font-medium", model && "text-foreground")}>
					{t(`runs.steps.kind.${step.kind}`, { defaultValue: clipText(step.kind, 40) })}
				</span>
				{step.event_type && <ReportedEventType value={step.event_type} testId={`step-row-${arm}-${step.seq}-event-type`} />}
				{reported ? (
					<ReportedSourceBadge channel={step.channel} testId={`step-row-${arm}-${step.seq}-reported`} />
				) : (
					<ToneBadge tone={sourceTone(step.source)} hint={t(`runs.steps.sourceHint.${step.source}`, { defaultValue: "" }) || undefined}>
						{sourceLabel(t, step.source)}
					</ToneBadge>
				)}
				{step.corroboration && <CorroborationBadge value={step.corroboration} testId={`step-row-${arm}-${step.seq}-corroboration`} />}
				<ToneBadge
					tone={PROOF_TONE[step.proof] ?? "neutral"}
					hint={t(`runs.steps.proofHint.${step.proof}`, { defaultValue: "" }) || undefined}
					testId={`step-row-${arm}-${step.seq}-proof`}
				>
					{t("runs.steps.proof", { level: t(`runs.steps.proofLevel.${step.proof}`, { defaultValue: clipText(step.proof, 20) }) })}
				</ToneBadge>
				{step.kind === "tool_request" && step.execution && (
					<ToneBadge
						tone={EXECUTION_TONE[step.execution] ?? "neutral"}
						hint={t(`runs.steps.executionHint.${step.execution}`, { defaultValue: "" }) || undefined}
						testId={`step-row-${arm}-${step.seq}-execution`}
					>
						{t(`runs.steps.execution.${step.execution}`, { defaultValue: clipText(step.execution, 40) })}
					</ToneBadge>
				)}
				{step.counted && (
					<ToneBadge tone="neutral" icon={CircleCheck} hint={t("runs.steps.countedHint")}>
						{t("runs.steps.countedBadge")}
					</ToneBadge>
				)}
				{step.conflict && (
					<ToneBadge tone="bad" icon={AlertTriangle} hint={t("runs.steps.conflictHint")} testId={`step-row-${arm}-${step.seq}-conflict`}>
						{t("runs.steps.conflict")}
					</ToneBadge>
				)}
				{step.is_error && <ToneBadge tone="bad">{t("runs.steps.error")}</ToneBadge>}
			</div>
			<div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 pl-10">
				{toolName && <span className="text-foreground font-mono break-all">{toolName}</span>}
				{summary && (
					<span className="text-foreground min-w-0 break-words" data-testid={`step-row-${arm}-${step.seq}-summary`}>
						{summary}
					</span>
				)}
				{path && <span className="text-muted-foreground font-mono break-all">{path}</span>}
				{step.op && <span className="text-muted-foreground">{clipText(step.op, 20)}</span>}
				{step.exit_code !== undefined && step.exit_code !== null && (
					<span className={cn("font-mono", step.exit_code !== 0 ? "text-chart-error-ink" : "text-muted-foreground")}>
						{t("runs.steps.exit", { code: step.exit_code })}
					</span>
				)}
				{step.pid ? <span className="text-muted-foreground font-mono">{t("runs.steps.pid", { pid: step.pid })}</span> : null}
				{step.attempt && step.attempt > 1 ? (
					<span className="text-muted-foreground">{t("runs.steps.attempt", { attempt: step.attempt })}</span>
				) : null}
				{step.duration_ms !== undefined && step.duration_ms !== null && (
					<span className="text-muted-foreground tabular-nums">{t("runs.steps.ms", { value: step.duration_ms })}</span>
				)}
				{step.witness_of ? <span className="text-muted-foreground">{t("runs.steps.witnessOf", { seq: step.witness_of })}</span> : null}
				{step.parent_seq ? (
					<span className="text-muted-foreground" data-testid={`step-row-${arm}-${step.seq}-parent`}>
						{t("runs.steps.reported.parent", { seq: step.parent_seq })}
					</span>
				) : null}
				{reportId && (
					<span className="text-muted-foreground" data-testid={`step-row-${arm}-${step.seq}-report-id`}>
						{t("runs.steps.reported.reportId")} <span className="font-mono break-all">{reportId}</span>
					</span>
				)}
				{step.witnessed_by && step.witnessed_by.length > 0 && (
					<span className="text-muted-foreground">
						{t("runs.steps.witnessedBy", { seqs: step.witnessed_by.map((seq) => `#${seq}`).join(", ") })}
					</span>
				)}
			</div>
			{conflict && (
				<p
					className="text-chart-error-ink flex min-w-0 items-baseline gap-1 pl-10 break-words"
					data-testid={`step-row-${arm}-${step.seq}-conflict-text`}
				>
					<span className="font-medium">{t("runs.steps.conflictHint")}:</span>
					<span>{conflict}</span>
				</p>
			)}
		</li>
	);
}

/** One line per observer and session: state, poll interval, coverage, records and drops, and why it is missing. */
export function ObserverStatusLine({ observers, arm, locale }: { observers: readonly ObserverStatus[]; arm: string; locale: string }) {
	const { t } = useTranslation("bench");
	if (observers.length === 0) {
		return (
			<p className="text-muted-foreground text-xs" data-testid={`step-observers-${arm}-none`}>
				{t("runs.steps.observers.none")}
			</p>
		);
	}
	return (
		<ul className="flex min-w-0 flex-col gap-1" aria-label={t("runs.steps.observers.title")} data-testid={`step-observers-${arm}`}>
			{observers.map((status, i) => (
				<li
					key={`${status.observer}-${status.session ?? 0}-${i}`}
					className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs"
					data-testid={`step-observer-${arm}-${status.observer}-${status.session ?? 0}`}
					data-state={status.state}
					data-degraded={observerDegraded(status)}
				>
					<span className="text-foreground font-medium">
						{t(`runs.steps.observers.name.${status.observer}`, { defaultValue: clipText(status.observer, 40) })}
					</span>
					{status.session ? <span>{t("runs.steps.session", { session: status.session })}</span> : null}
					<ToneBadge tone={OBSERVER_TONE[status.state] ?? "neutral"}>
						{t(`runs.steps.observers.state.${status.state}`, { defaultValue: clipText(status.state, 20) })}
					</ToneBadge>
					{status.interval_ms ? <span>{t("runs.steps.observers.interval", { value: status.interval_ms })}</span> : null}
					{status.observer_coverage && (
						<span>
							{t("runs.steps.observers.coverage")} <span className="text-foreground">{clipText(status.observer_coverage, 60)}</span>
						</span>
					)}
					{status.state !== "off" && (
						<span>{t("runs.steps.observers.records", { count: status.records, value: formatCount(status.records, locale) })}</span>
					)}
					{(status.dropped ?? 0) > 0 && (
						<span className="text-chart-warning-ink">
							{t("runs.steps.observers.dropped", { value: formatCount(status.dropped ?? 0, locale) })}
						</span>
					)}
					{status.pids_allocated ? (
						<span>{t("runs.steps.observers.pids", { seen: status.pids_seen ?? 0, allocated: status.pids_allocated })}</span>
					) : null}
					{status.reason && (
						<ObserverReason
							reason={status.reason}
							name={t(`runs.steps.observers.name.${status.observer}`, { defaultValue: clipText(status.observer, 40) })}
						/>
					)}
				</li>
			))}
		</ul>
	);
}

/** Known observer failure reasons as plain words (M4-56); the raw text stays behind Details. Null for an unknown reason. */
export function observerReasonKey(reason: string): "observerStartFailed" | null {
	return /observer exited before it was ready/i.test(reason) ? "observerStartFailed" : null;
}

/**
 * Why an observer is off or missing: a known reason in plain words, muted (a watcher that could
 * not start does not fail the run), with the raw text, often a container error, in a Details
 * disclosure. The reason is agent-influenced text: clipped, never a translation key.
 */
function ObserverReason({ reason, name }: { reason: string; name: string }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4b");
	const key = observerReasonKey(reason);
	if (!key) {
		return (
			<span>
				{t("runs.steps.observers.reason")} <span>{clipText(reason, 160)}</span>
			</span>
		);
	}
	return (
		<span className="inline-flex flex-wrap items-baseline gap-x-2">
			<span>{tu(`observer.${key}`, { name })}</span>
			<details className="inline">
				<summary className="text-primary cursor-pointer">{tu("observer.details")}</summary>
				<span className="font-mono break-all">{clipText(reason, 400)}</span>
			</details>
		</span>
	);
}

const COLLECTOR_STATES = ["on", "off", "missing"] as const;

/**
 * One line on what the agent reported about itself: how many events were kept, on which channels
 * and of which types, how many were dropped, and why the collector is missing. Marked
 * agent-reported like the reported rows. Channel and type keys and the reason are agent-influenced:
 * clipped React text, never a translation key.
 */
export function AgentEventsLine({
	status,
	arm,
	locale,
	blocking = false,
}: {
	status: AgentEventStatus;
	arm: string;
	locale: string;
	/** True only when the run failed because the collector was missing; otherwise a missing collector is a quiet note (M4-56). */
	blocking?: boolean;
}) {
	const { t } = useTranslation("bench");
	const kept = safeCount(status.kept);
	const dropped = safeCount(status.dropped);
	const channels = eventEntries(status.channels);
	const kinds = eventEntries(status.kinds);
	const collector = (COLLECTOR_STATES as readonly string[]).includes(status.collector) ? status.collector : undefined;
	const missing = collector === "missing" && blocking;
	const reason = clipText(status.reason, 160);
	return (
		<p
			className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs"
			aria-label={t("runs.steps.agentEvents.label")}
			data-testid={`step-agent-events-${arm}`}
			data-collector={collector ?? "unknown"}
			data-kept={kept}
		>
			<ToneBadge
				tone={sourceTone(REPORTED_SOURCE)}
				icon={Megaphone}
				hint={t("runs.steps.sourceHint.agent_reported")}
				className="border-dashed"
				testId={`step-agent-events-${arm}-badge`}
			>
				{t("runs.steps.source.agent_reported")}
			</ToneBadge>
			{kept > 0 ? (
				<span className="text-foreground font-medium">
					{t("runs.steps.agentEvents.kept", { count: kept, value: formatCount(kept, locale) })}
				</span>
			) : (
				<span className="text-foreground">{t("runs.steps.agentEvents.nothing")}</span>
			)}
			{kept > 0 && channels.entries.length > 0 && (
				<span data-testid={`step-agent-events-${arm}-channels`}>
					{t("runs.steps.agentEvents.via")}{" "}
					<EventCounts
						counts={channels}
						locale={locale}
						label={(key) => {
							const known = knownChannel(key);
							return known ? t(`runs.steps.reported.channel.${known}`) : clipText(key, 24);
						}}
					/>
				</span>
			)}
			{kept > 0 && kinds.entries.length > 0 && (
				<span data-testid={`step-agent-events-${arm}-kinds`}>
					{t("runs.steps.agentEvents.types")}{" "}
					<EventCounts
						counts={kinds}
						locale={locale}
						label={(key) => {
							const known = knownEventType(key);
							return known ? t(`runs.steps.reported.eventType.${known}`) : clipText(key, 40);
						}}
					/>
				</span>
			)}
			{dropped > 0 && (
				<span
					className="text-chart-warning-ink"
					title={t("runs.steps.agentEvents.droppedHint")}
					data-testid={`step-agent-events-${arm}-dropped`}
				>
					{t("runs.steps.agentEvents.dropped", { value: formatCount(dropped, locale) })}
				</span>
			)}
			{collector && collector !== "on" && (
				<ToneBadge tone={missing ? "bad" : "neutral"} testId={`step-agent-events-${arm}-collector`}>
					{t(`runs.steps.agentEvents.collector.${collector}`)}
				</ToneBadge>
			)}
			{reason && (
				<span className={missing ? "text-chart-error-ink" : undefined} data-testid={`step-agent-events-${arm}-reason`}>
					{t("runs.steps.agentEvents.reason")} <span>{reason}</span>
				</span>
			)}
		</p>
	);
}

/** "OTLP 3, native 1, +2 more": labels from the caller, counts formatted, keys never rendered as anything but text. */
function EventCounts({
	counts,
	locale,
	label,
}: {
	counts: { entries: [string, number][]; rest: number };
	locale: string;
	label: (key: string) => string;
}) {
	const { t } = useTranslation("bench");
	const parts = counts.entries.map(([key, n]) => `${label(key)} ${formatCount(n, locale)}`);
	if (counts.rest > 0) parts.push(t("runs.steps.agentEvents.more", { value: formatCount(counts.rest, locale) }));
	return <span className="text-foreground">{parts.join(", ")}</span>;
}