import "./experimentsI18n";

import { filterExperiments } from "@/components/experiments/experimentList";
import {
	humanExperimentName,
	isDebugExperiment,
	isHiddenByDefault,
	suiteName,
	systemExperiment,
} from "@/components/experiments/experiments.utils";
import { useEvaluationNames } from "@/components/experiments/useEvaluationNames";
import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { FilterSelect, FilterTokens, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import type { ExperimentState, ExperimentStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import {
	Brain,
	Check,
	Code2,
	Cpu,
	FlaskConical,
	Globe,
	type LucideIcon,
	MessagesSquare,
	Mic,
	Monitor,
	Search,
	ShieldCheck,
	SquareTerminal,
	Users,
	Wrench,
	X,
} from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

const SUITE_ICONS: readonly [RegExp, LucideIcon][] = [
	[/terminal|shell|bash|cli/, SquareTerminal],
	[/memory|recall|notes/, Brain],
	[/computer|desktop|gui|cua/, Monitor],
	[/browser|web/, Globe],
	[/voice|speech|audio/, Mic],
	[/support|chat|customer/, MessagesSquare],
	[/mcp|tool/, Wrench],
	[/research|search/, Search],
	[/safety|security|guard|red/, ShieldCheck],
	[/multi|swarm|team/, Users],
	[/gpu|cuda|kernel/, Cpu],
	[/cod|swe|repo|bug/, Code2],
];

/** The category icon for a suite id, guessed from its name. */
export function suiteIcon(suite: string | undefined): LucideIcon {
	const id = suiteName(suite).toLowerCase();
	return SUITE_ICONS.find(([re]) => re.test(id))?.[1] ?? FlaskConical;
}

type Tone = "green" | "red" | "amber" | "blue" | "grey";

const TONE: Record<Tone, string> = {
	green: "bg-chart-success/10 text-chart-success-ink",
	red: "bg-chart-error/10 text-chart-error-ink",
	amber: "bg-chart-warning/10 text-chart-warning-ink",
	blue: "bg-primary/10 text-primary",
	grey: "bg-muted text-muted-foreground",
};

/** Verdict words and color for a decision. Unknown values read as pending. Pure. */
export function verdictTone(decision: string | undefined): {
	key: "improved" | "regressed" | "inconclusive" | "invalid" | "score" | "pending";
	tone: Tone;
} {
	switch (decision) {
		case "improved":
			return { key: "improved", tone: "green" };
		case "regressed":
			return { key: "regressed", tone: "red" };
		case "inconclusive":
			return { key: "inconclusive", tone: "grey" };
		case "invalid":
			return { key: "invalid", tone: "amber" };
		case "score":
			return { key: "score", tone: "blue" };
		default:
			return { key: "pending", tone: "grey" };
	}
}

const STATE_TONE: Record<ExperimentState, Tone> = {
	running: "blue",
	analyzing: "blue",
	created: "grey",
	done: "green",
	failed: "red",
	cancelled: "grey",
};

/**
 * Status choices. Each covers one or more experiment states; the URL keeps the first one as
 * ?state= (running for Running), so older links keep working.
 */
const STATUSES: readonly {
	key: "active" | "done" | "failed" | "cancelled" | "created";
	states: readonly ExperimentState[];
}[] = [
	{ key: "active", states: ["running", "analyzing"] },
	{ key: "done", states: ["done"] },
	{ key: "failed", states: ["failed"] },
	{ key: "cancelled", states: ["cancelled"] },
	{ key: "created", states: ["created"] },
];

/** True when an experiment state matches a ?state= value: a status choice's states, else that exact state. Pure. */
export function stateMatches(state: string, filter: string | null | undefined): boolean {
	if (!filter) return true;
	const choice = STATUSES.find((c) => c.states[0] === filter);
	return choice ? choice.states.includes(state as ExperimentState) : state === filter;
}

function Pill({ tone, children, testId }: { tone: Tone; children: React.ReactNode; testId?: string }) {
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", TONE[tone])}
			data-testid={testId}
		>
			{children}
		</span>
	);
}

function StatePill({ state, testId }: { state: ExperimentState; testId?: string }) {
	const { t } = useTranslation("experimentsUx");
	const live = state === "running" || state === "analyzing";
	return (
		<Pill tone={STATE_TONE[state] ?? "grey"} testId={testId}>
			{live ? (
				<span className="relative flex size-2" aria-hidden="true">
					<span className="bg-primary absolute inline-flex size-full animate-ping rounded-full opacity-60" />
					<span className="bg-primary relative inline-flex size-2 rounded-full" />
				</span>
			) : state === "done" ? (
				<Check className="size-3" aria-hidden="true" />
			) : state === "failed" ? (
				<X className="size-3" aria-hidden="true" />
			) : (
				<span className="size-1.5 rounded-full bg-current opacity-60" aria-hidden="true" />
			)}
			{t(`status.${state}`, { defaultValue: state })}
		</Pill>
	);
}

function VerdictPill({ decision, testId }: { decision: string | undefined; testId?: string }) {
	const { t } = useTranslation("experimentsUx");
	const v = verdictTone(decision);
	if (v.key === "pending") return <span className="text-muted-foreground text-xs">{t("verdict.pending")}</span>;
	return (
		<Pill tone={v.tone} testId={testId}>
			{t(`verdict.${v.key}`)}
		</Pill>
	);
}

function RunsProgress({ e }: { e: ExperimentStatus }) {
	const { t } = useTranslation("experimentsUx");
	const done = e.slots.done + e.slots.failed;
	const total = e.slots.total;
	const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
	return (
		<span className="flex min-w-24 flex-col items-end gap-1" aria-label={t("runsAria", { done, total })}>
			<span className="text-xs tabular-nums">{t("runs", { done, total })}</span>
			<span className="bg-muted h-1 w-full overflow-hidden rounded-full">
				<span
					className={cn("block h-full rounded-full", e.slots.failed > 0 ? "bg-chart-warning" : "bg-primary")}
					style={{ width: `${pct}%` }}
				/>
			</span>
		</span>
	);
}

/** /experiments body: one toolbar (search, status, benchmark), and one row per experiment with a readable name. */
export function ExperimentBoard() {
	const { t } = useTranslation("experimentsUx");
	const { t: tb } = useTranslation("bench");
	const locale = getLocale();
	const list = useListExperimentsQuery();
	const [filters, setFilters] = useQueryStates(
		{ q: parseAsString, state: parseAsString, suite: parseAsString, all: parseAsString },
		{ history: "replace", throttleMs: 200 },
	);
	const everything = useMemo(() => list.data?.experiments ?? [], [list.data]);
	// Automatic checks, practice runs and the single-setup benchmarks of an evaluation are not
	// comparisons: hidden unless asked for (URL all=1), so the list matches its subtitle.
	const showSystem = filters.all === "1";
	const systemCount = useMemo(() => everything.filter(isHiddenByDefault).length, [everything]);
	const all = useMemo(() => (showSystem ? everything : everything.filter((e) => !isHiddenByDefault(e))), [everything, showSystem]);
	const evalNames = useEvaluationNames();
	const named = useMemo(
		() =>
			new Map(all.map((e) => [e.exp_id, humanExperimentName(e, (k, o) => String(t(k as never, (o ?? {}) as never)), evalNames)] as const)),
		[all, t, evalNames],
	);
	// The suite filter applies first, so each status choice can say how many it would show.
	const inSuite = useMemo(() => filterExperiments(all, { suite: filters.suite }), [all, filters.suite]);
	const statusCounts = useMemo(
		() => new Map(STATUSES.map((c) => [c.key, inSuite.filter((e) => c.states.includes(e.state)).length] as const)),
		[inSuite],
	);
	const shown = useMemo(() => {
		const q = filters.q?.trim().toLowerCase() ?? "";
		// The id and typed name match in filterExperiments; the readable name and the suite match here.
		const base = inSuite.filter((e) => stateMatches(e.state, filters.state));
		return q
			? base.filter(
					(e) =>
						e.exp_id.toLowerCase().includes(q) ||
						e.name.toLowerCase().includes(q) ||
						(named.get(e.exp_id) ?? "").toLowerCase().includes(q) ||
						(e.suite ?? "").toLowerCase().includes(q),
				)
			: base;
	}, [inSuite, filters.q, filters.state, named]);
	const suites = useMemo(() => [...new Set(all.map((e) => e.suite).filter((s): s is string => Boolean(s)))].sort(), [all]);
	const clear = () => void setFilters({ q: null, state: null, suite: null });
	const statusKey = STATUSES.find((c) => c.states[0] === filters.state)?.key;
	const tokens = (
		[
			filters.state && {
				key: "state",
				label: `${t("col.status")}: ${statusKey ? t(`chips.${statusKey}`) : filters.state}`,
				onRemove: () => void setFilters({ state: null }),
			},
			filters.suite && {
				key: "suite",
				label: `${t("chips.benchmark")}: ${suiteName(filters.suite)}`,
				onRemove: () => void setFilters({ suite: null }),
			},
		] as (FilterToken | false | "" | null | undefined)[]
	).filter((x): x is FilterToken => Boolean(x));

	if (list.isLoading) return <BlockSkeleton rows={6} testId="experiment-list-loading" />;
	if (list.error || !list.data) {
		return (
			<LoadError
				title={tb("experiments.list.loadError")}
				error={list.error}
				onRetry={() => void list.refetch()}
				testId="experiment-list-error"
			/>
		);
	}
	if (everything.length === 0) {
		return (
			<EmptyState
				icon={FlaskConical}
				message={tb("experiments.list.empty")}
				action={{ label: tb("experiments.list.getStarted"), to: "/quickstart" }}
				testId="experiment-list-empty"
			/>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="experiment-list">
			<div className="flex flex-col gap-2" role="search" aria-label={tb("experiments.list.filtersLabel")}>
				<div className="flex flex-wrap items-center gap-2" role="toolbar" data-testid="experiment-toolbar">
					<ToolbarSearch
						value={filters.q ?? ""}
						onChange={(v) => void setFilters({ q: v || null })}
						placeholder={t("searchPlaceholder")}
						label={tb("experiments.list.search")}
						testId="experiment-search"
					/>
					<FilterSelect
						label={t("col.status")}
						allLabel={t("chips.all")}
						options={STATUSES.map((c) => ({ value: c.states[0], label: `${t(`chips.${c.key}`)} (${statusCounts.get(c.key) ?? 0})` }))}
						value={filters.state}
						onChange={(v) => void setFilters({ state: v })}
						testId="experiment-filter-status"
					/>
					{(suites.length > 1 || filters.suite) && (
						<FilterSelect
							label={t("chips.benchmark")}
							allLabel={t("chips.all")}
							options={suites.map((s) => ({ value: s, label: suiteName(s) }))}
							value={filters.suite}
							onChange={(v) => void setFilters({ suite: v })}
							testId="experiment-filter-suite"
						/>
					)}
					{systemCount > 0 && (
						<label className="text-muted-foreground ml-auto inline-flex items-center gap-2 text-xs" title={t("system.toggleHint")}>
							<input
								type="checkbox"
								checked={showSystem}
								onChange={(ev) => void setFilters({ all: ev.target.checked ? "1" : null })}
								className="accent-primary size-3.5"
								data-testid="experiment-show-system"
							/>
							{t("system.toggle", { count: systemCount })}
						</label>
					)}
				</div>
				<FilterTokens tokens={tokens} onClear={clear} testId="experiment-tokens" />
			</div>

			{shown.length === 0 ? (
				<EmptyState
					icon={Search}
					message={tb("experiments.list.noMatch")}
					action={{ label: tb("runs.list.clear"), onClick: clear }}
					compact
					testId="experiment-list-no-match"
				/>
			) : (
				<div className="bg-card overflow-hidden rounded-xl border">
					<div
						className="text-muted-foreground bg-muted/40 hidden grid-cols-[minmax(0,1fr)_8rem_10rem_7rem_6rem_7rem] items-center gap-4 border-b px-5 py-2.5 text-xs font-medium md:grid"
						aria-hidden="true"
					>
						<span>{t("col.experiment")}</span>
						<span>{t("col.status")}</span>
						<span>{t("col.verdict")}</span>
						<span className="text-right">{t("col.runs")}</span>
						<span className="text-right">{t("col.cost")}</span>
						<span className="text-right">{t("col.when")}</span>
					</div>
					<ul className="divide-y" data-testid="experiment-table" aria-label={tb("experiments.list.tableLabel")}>
						{shown.map((e) => {
							const Icon = suiteIcon(e.suite);
							const when = e.started_at ?? e.frozen_at;
							const title = named.get(e.exp_id) ?? e.exp_id;
							return (
								<li
									key={e.exp_id}
									className="hover:bg-muted/40 relative flex flex-col gap-3 px-5 py-4 transition-colors md:grid md:grid-cols-[minmax(0,1fr)_8rem_10rem_7rem_6rem_7rem] md:items-center md:gap-4"
									data-testid={`experiment-row-${e.exp_id}`}
								>
									<span className="flex min-w-0 items-center gap-3">
										<span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-lg">
											<Icon className="size-4" aria-hidden="true" />
										</span>
										<span className="flex min-w-0 flex-col">
											<EntityLink
												kind="experiment"
												id={e.exp_id}
												className="text-foreground truncate font-sans text-sm font-semibold after:absolute after:inset-0"
												testId={`experiment-row-${e.exp_id}-name`}
											>
												<span title={title}>{title}</span>
											</EntityLink>
											<span className="text-muted-foreground truncate text-xs">
												{suiteName(e.suite)}
												{systemExperiment(e) ? (
													<span> · {t("system.tag")}</span>
												) : isDebugExperiment(e) ? (
													<span> · {t("debug.tag")}</span>
												) : (
													e.arms && e.arms.length > 0 && <span className="font-mono"> · {e.arms.join(" vs ")}</span>
												)}
												<span className="font-mono text-[10px] opacity-50"> · {e.exp_id}</span>
											</span>
										</span>
									</span>
									<span className="flex flex-wrap items-center gap-2 md:contents">
										<span>
											<StatePill state={e.state} testId={`experiment-row-${e.exp_id}-state`} />
										</span>
										<span>
											<VerdictPill decision={e.decision} testId={`experiment-row-${e.exp_id}-decision`} />
										</span>
										<span className="ml-auto md:ml-0 md:flex md:justify-end">
											<RunsProgress e={e} />
										</span>
										<span className="text-right text-sm">
											<ValueCell value={e.spent_usd} kind="usd" />
										</span>
										<span
											className="text-muted-foreground text-right text-xs whitespace-nowrap tabular-nums"
											title={formatWhen(when, locale) ?? undefined}
										>
											{formatRelative(when, locale) ?? ""}
										</span>
									</span>
								</li>
							);
						})}
					</ul>
				</div>
			)}
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="experiment-list-count">
				{t("count", { shown: shown.length, total: all.length })}
				{!showSystem && systemCount > 0 && ` ${t("system.hidden", { count: systemCount })}`}
			</p>
		</div>
	);
}