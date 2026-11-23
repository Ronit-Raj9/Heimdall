import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Input } from "@/components/ui/input";
import { useGetRunEventsQuery } from "@/lib/store/apis/benchApi";
import { useGetRunStepsQuery } from "@/lib/store/apis/runStepsApi";
import { useGetRunCallsQuery } from "@/lib/store/apis/runDebugApi"; // X20b step inspector
import type { RunCall } from "@/lib/types/runDebug";
import type { BenchEvent, RunSteps, StepRecord } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Bot, ChevronRight, CircleDot, FilePen, Search, ShieldCheck, SquareTerminal, Wrench, type LucideIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { callsByRequest, stepIndex } from "./debug/debug.utils"; // X20b
import { EditedCallBadge, ReplayBadge } from "./debug/replayBadge";
import { StepInspector } from "./debug/stepInspector";
import { AgentEventsLine, ObserverStatusLine } from "./stepTimeline";
import { clipText, stepTime } from "./stepTimeline.utils";
import {
	TRACE_FILTERS,
	filterCounts,
	filterTrace,
	formatDuration,
	isErrorStep,
	stepIO,
	tokenizeJson,
	traceKind,
	usageByRequest,
	type JsonTokenKind,
	type StepUsage,
	type TraceFilter,
	type TraceKind,
} from "./traceView.utils";

/** Poll interval of a live (unsealed) run's trace. */
export const LIVE_POLL_MS = 2000;

const KIND_ICON: Record<TraceKind, LucideIcon> = {
	model: Bot,
	tool: Wrench,
	command: SquareTerminal,
	file: FilePen,
	verifier: ShieldCheck,
	other: CircleDot,
};

const KIND_TONE: Record<TraceKind, string> = {
	model: "bg-chart-info/15 text-chart-info-ink",
	tool: "bg-chart-warning/15 text-chart-warning-ink",
	command: "bg-muted text-foreground",
	file: "bg-chart-success/15 text-chart-success-ink",
	verifier: "bg-primary/10 text-primary",
	other: "bg-muted text-muted-foreground",
};

/**
 * One run's trace, fetched by run id. While the run is live (not sealed) the steps and events are
 * polled every LIVE_POLL_MS and new rows append with a Live pulse.
 */
export function RunTraceView({
	runId,
	arm,
	live = false,
	canFork = false,
	initialStep,
}: {
	runId: string;
	arm: string;
	live?: boolean;
	/** X20b: offer Fork from this step (a sealed run without a fault). */
	canFork?: boolean;
	/** N51: the step a link asked for (?step=), selected and scrolled into view once. */
	initialStep?: number;
}) {
	const { t } = useTranslation("bench");
	const poll = {
		pollingInterval: live ? LIVE_POLL_MS : 0,
		skipPollingIfUnfocused: true,
	};
	const steps = useGetRunStepsQuery(runId, poll);
	const events = useGetRunEventsQuery(runId, poll);
	const calls = useGetRunCallsQuery(runId, poll); // X20b: replay labels and the step inspector
	if (steps.isLoading) return <BlockSkeleton rows={6} testId={`trace-${arm}-loading`} />;
	if (steps.error || !steps.data) {
		return (
			<LoadError title={t("runs.steps.loadError")} error={steps.error} onRetry={() => void steps.refetch()} testId={`trace-${arm}-error`} />
		);
	}
	return (
		<TraceView
			data={steps.data}
			events={events.data?.events}
			arm={arm}
			live={live}
			runId={runId}
			calls={calls.data?.calls}
			firstDivergence={calls.data?.first_divergence?.call}
			canFork={canFork}
			initialStep={initialStep}
		/>
	);
}

export interface TraceViewProps {
	data: RunSteps;
	events?: readonly BenchEvent[];
	arm: string;
	live?: boolean;
	initialFilter?: TraceFilter;
	/** X20b: with a run id the trace gets the step inspector (j/k stepping, raw calls, fork). */
	runId?: string;
	calls?: readonly RunCall[];
	firstDivergence?: number;
	canFork?: boolean;
	/** N51: select this step on open (all steps shown) and scroll it into view once; a seq not in the trace is ignored. */
	initialStep?: number;
}

/**
 * The run trace as a vertical list, one row per step: icon by kind, summary, duration, token and
 * cost chips, a small proof badge, and a collapsible input/output panel with highlighted JSON.
 * Agent-influenced strings are React text nodes only, clipped on the row.
 */
export function TraceView({
	data,
	events,
	arm,
	live = false,
	initialFilter = "all",
	runId,
	calls,
	firstDivergence,
	canFork = true,
	initialStep,
}: TraceViewProps) {
	const { t } = useTranslation("runTrace");
	const { t: tb } = useTranslation("bench");
	const { t: td } = useTranslation("runDebug");
	const [filter, setFilter] = useState<TraceFilter>(initialStep ? "all" : initialFilter);
	const [query, setQuery] = useState("");
	const [autoScroll, setAutoScroll] = useState(true);
	const counts = useMemo(() => filterCounts(data.steps), [data.steps]);
	const visible = useMemo(() => filterTrace(data.steps, filter, query), [data.steps, filter, query]);
	const usage = useMemo(() => usageByRequest(events), [events]);
	const callMap = useMemo(() => callsByRequest(calls), [calls]);
	const [selected, setSelected] = useState<number | null>(initialStep ?? null);
	const scrolledRef = useRef(false);
	useEffect(() => {
		if (scrolledRef.current || initialStep === undefined || !visible.some((s) => s.seq === initialStep)) return;
		scrolledRef.current = true;
		document.querySelector(`[data-testid="trace-row-${arm}-${initialStep}"]`)?.scrollIntoView?.({ block: "center" });
	}, [initialStep, visible, arm]);
	const selIndex = selected === null ? -1 : visible.findIndex((s) => s.seq === selected);
	const selStep = selIndex >= 0 ? visible[selIndex] : null;
	useEffect(() => {
		if (!runId) return;
		const onKey = (e: KeyboardEvent) => {
			const el = e.target as HTMLElement | null;
			if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
			if (e.key === "j" || e.key === "k") {
				const next = stepIndex(selIndex, e.key === "j" ? 1 : -1, visible.length);
				if (next >= 0) {
					setSelected(visible[next].seq);
					document.querySelector(`[data-testid="trace-row-${arm}-${visible[next].seq}"]`)?.scrollIntoView?.({ block: "nearest" });
				}
			} else if (e.key === "Escape") {
				setSelected(null);
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [runId, selIndex, visible, arm]);
	const firstError = visible.find(isErrorStep);
	const endRef = useRef<HTMLDivElement>(null);
	const seenRef = useRef<Set<number> | null>(null);
	if (seenRef.current === null) seenRef.current = new Set(data.steps.map((s) => s.seq));
	const fresh = useMemo(() => {
		const seen = seenRef.current ?? new Set<number>();
		return new Set(data.steps.filter((s) => !seen.has(s.seq)).map((s) => s.seq));
	}, [data.steps]);

	useEffect(() => {
		for (const step of data.steps) seenRef.current?.add(step.seq);
		if (live && autoScroll && fresh.size > 0) endRef.current?.scrollIntoView?.({ block: "end", behavior: "smooth" });
	}, [data.steps, live, autoScroll, fresh]);

	if (!data.available && !live) {
		return <EmptyNote testId={`trace-${arm}-unavailable`}>{tb("runs.steps.unavailable")}</EmptyNote>;
	}

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid={`trace-${arm}`} data-live={live}>
			<div className="flex flex-wrap items-center gap-2">
				<div role="group" aria-label={t("filters.label")} className="bg-muted/60 flex flex-wrap gap-0.5 rounded-lg p-0.5">
					{TRACE_FILTERS.map((id) => (
						<button
							key={id}
							type="button"
							aria-pressed={filter === id}
							onClick={() => setFilter(id)}
							data-testid={`trace-filter-${id}`}
							className={cn(
								"focus-visible:ring-ring/50 inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition-colors focus-visible:ring-[3px] focus-visible:outline-none",
								filter === id ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
							)}
						>
							{t(`filters.${id}`)}
							<span className="text-muted-foreground tabular-nums">{counts[id]}</span>
						</button>
					))}
				</div>
				<div className="relative min-w-[10rem] flex-1 sm:max-w-xs">
					<Search
						className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2"
						aria-hidden="true"
					/>
					<Input
						value={query}
						onChange={(e) => setQuery(e.target.value)}
						placeholder={t("search")}
						aria-label={t("search")}
						className="h-8 pl-8 text-xs"
						data-testid="trace-search"
					/>
				</div>
				{live && (
					<div className="ml-auto flex items-center gap-3 text-xs">
						<span className="text-chart-info-ink inline-flex items-center gap-1.5 font-medium" data-testid="trace-live">
							<span className="relative flex size-2">
								<span className="bg-chart-info absolute inline-flex size-full animate-ping rounded-full opacity-75" />
								<span className="bg-chart-info relative inline-flex size-2 rounded-full" />
							</span>
							{t("live")}
						</span>
						<label className="text-muted-foreground inline-flex cursor-pointer items-center gap-1.5">
							<input
								type="checkbox"
								checked={autoScroll}
								onChange={(e) => setAutoScroll(e.target.checked)}
								data-testid="trace-autoscroll"
							/>
							{t("autoScroll")}
						</label>
					</div>
				)}
			</div>
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground text-xs" data-testid={`trace-${arm}-summary`}>
					{t("shown", { shown: visible.length, total: data.steps.length })}
				</p>
				{runId && firstError && (
					<button
						type="button"
						className="text-chart-error-ink text-xs underline-offset-2 hover:underline"
						onClick={() => setSelected(firstError.seq)}
						data-testid={`trace-${arm}-first-error`}
					>
						{td("inspector.firstError")}
					</button>
				)}
				{firstDivergence !== undefined && (
					<p className="text-chart-error-ink text-xs font-medium" data-testid={`trace-${arm}-divergence`}>
						{td("replay.firstDivergence", { call: firstDivergence })}
					</p>
				)}
			</div>
			{data.steps.length === 0 ? (
				<EmptyNote testId={`trace-${arm}-empty`}>{live ? t("waiting") : t("empty")}</EmptyNote>
			) : visible.length === 0 ? (
				<EmptyNote testId={`trace-${arm}-filtered`}>{t("noMatch")}</EmptyNote>
			) : (
				<div className={cn("grid min-w-0 gap-4", runId && "lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]")}>
					<ol className="flex min-w-0 flex-col divide-y self-start rounded-xl border" aria-label={tb("runs.steps.label", { arm })}>
						{visible.map((step) => (
							<TraceRow
								key={step.seq}
								step={step}
								arm={arm}
								usage={step.request_id ? usage.get(step.request_id) : undefined}
								fresh={fresh.has(step.seq)}
								call={step.request_id ? callMap.get(step.request_id) : undefined}
								selected={selected === step.seq}
								onSelect={runId ? () => setSelected(step.seq) : undefined}
							/>
						))}
					</ol>
					{runId && (
						<div className="min-w-0 lg:sticky lg:top-4 lg:self-start">
							<StepInspector
								runId={runId}
								step={selStep}
								call={selStep?.request_id ? callMap.get(selStep.request_id) : undefined}
								canFork={canFork}
								onClose={() => setSelected(null)}
							/>
						</div>
					)}
				</div>
			)}
			<div ref={endRef} />
			<details className="text-xs" data-testid={`trace-${arm}-observers`}>
				<summary className="text-muted-foreground hover:text-foreground cursor-pointer">{tb("runs.steps.observers.title")}</summary>
				<div className="mt-2 flex flex-col gap-2">
					<ObserverStatusLine observers={data.observers} arm={arm} locale="en" />
					{data.agent_events && <AgentEventsLine status={data.agent_events} arm={arm} locale="en" />}
				</div>
			</details>
		</div>
	);
}

function TraceRow({
	step,
	arm,
	usage,
	fresh,
	call,
	selected = false,
	onSelect,
}: {
	step: StepRecord;
	arm: string;
	usage?: StepUsage;
	fresh: boolean;
	call?: RunCall;
	selected?: boolean;
	onSelect?: () => void;
}) {
	const { t } = useTranslation("runTrace");
	const { t: tb } = useTranslation("bench");
	const [open, setOpen] = useState(false);
	const kind = traceKind(step);
	const Icon = KIND_ICON[kind];
	const error = isErrorStep(step);
	const label = tb(`runs.steps.kind.${step.kind}`, {
		defaultValue: t(`kind.${kind}`),
	});
	const io = open ? stepIO(step) : null;
	return (
		<li
			className={cn(
				"min-w-0",
				fresh && "animate-in fade-in bg-chart-info/5",
				error && "bg-chart-error/5",
				selected && "bg-primary/5 ring-primary/40 ring-1 ring-inset",
			)}
			data-selected={selected}
			data-testid={`trace-row-${arm}-${step.seq}`}
			data-kind={kind}
			data-error={error}
		>
			<button
				type="button"
				onClick={() => {
					setOpen((v) => !v);
					onSelect?.();
				}}
				aria-expanded={open}
				aria-label={t("expand", { seq: step.seq })}
				className="hover:bg-muted/40 focus-visible:ring-ring/50 flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left transition-colors focus-visible:ring-[3px] focus-visible:outline-none"
			>
				<ChevronRight
					className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-90")}
					aria-hidden="true"
				/>
				<span
					className={cn(
						"flex size-7 shrink-0 items-center justify-center rounded-md",
						error ? "bg-chart-error/15 text-chart-error-ink" : KIND_TONE[kind],
					)}
				>
					<Icon className="size-3.5" aria-hidden="true" />
				</span>
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="flex min-w-0 items-center gap-2 text-sm">
						<span className="font-medium">{label}</span>
						{step.tool_name && <span className="text-muted-foreground truncate font-mono text-xs">{clipText(step.tool_name, 60)}</span>}
					</span>
					{step.summary && (
						<span className="text-muted-foreground truncate text-xs" title={clipText(step.summary, 400)}>
							{clipText(step.summary, 160)}
						</span>
					)}
				</span>
				{call?.replay === "edited" && <EditedCallBadge call={call.call} testId={`trace-row-${arm}-${step.seq}-replay`} />}
				<span className="hidden shrink-0 items-center gap-1.5 sm:flex">
					{call?.replay && call.replay !== "edited" && <ReplayBadge label={call.replay} testId={`trace-row-${arm}-${step.seq}-replay`} />}
					{usage && usage.tokens > 0 && (
						<Chip testId={`trace-row-${arm}-${step.seq}-tokens`}>{t("tokens", { value: usage.tokens.toLocaleString() })}</Chip>
					)}
					{usage && usage.usd !== null && (
						<Chip testId={`trace-row-${arm}-${step.seq}-cost`}>{t("cost", { value: usage.usd.toFixed(4) })}</Chip>
					)}
					<span
						className="text-muted-foreground rounded-full border px-1.5 text-[10px] leading-4"
						title={tb(`runs.steps.proofHint.${step.proof}`, { defaultValue: tb("runs.steps.proof", { level: step.proof }) })}
						data-testid={`trace-row-${arm}-${step.seq}-proof`}
					>
						{tb(`runs.steps.proofLevel.${step.proof}`, {
							defaultValue: clipText(step.proof, 20),
						})}
					</span>
				</span>
				<span className="text-muted-foreground w-16 shrink-0 text-right font-mono text-xs tabular-nums">
					{step.duration_ms !== undefined && step.duration_ms !== null ? formatDuration(step.duration_ms) : stepTime(step.ts)}
				</span>
			</button>
			{io && (
				<div className="grid min-w-0 gap-3 px-3 pb-3 pl-12 md:grid-cols-2" data-testid={`trace-row-${arm}-${step.seq}-detail`}>
					<JsonBlock title={t("input")} value={io.input} />
					<JsonBlock title={t("output")} value={io.output} />
				</div>
			)}
		</li>
	);
}

function Chip({ children, testId }: { children: React.ReactNode; testId?: string }) {
	return (
		<span className="bg-muted text-muted-foreground rounded-full px-2 py-0.5 font-mono text-[11px] tabular-nums" data-testid={testId}>
			{children}
		</span>
	);
}

const TOKEN_CLASS: Record<JsonTokenKind, string> = {
	key: "text-chart-info-ink",
	string: "text-chart-success-ink",
	number: "text-chart-warning-ink",
	literal: "text-primary",
	punct: "text-muted-foreground",
};

/** Pretty JSON with token colors. Text nodes only. */
export function JsonBlock({ title, value }: { title: string; value: unknown }) {
	const tokens = useMemo(() => tokenizeJson(value), [value]);
	return (
		<div className="flex min-w-0 flex-col gap-1">
			<span className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">{title}</span>
			<pre className="bg-muted/40 max-h-72 overflow-auto rounded-lg border p-3 font-mono text-[11px] leading-relaxed break-all whitespace-pre-wrap">
				{tokens.map((tok, i) => (
					<span key={i} className={TOKEN_CLASS[tok.kind]}>
						{tok.text}
					</span>
				))}
			</pre>
		</div>
	);
}