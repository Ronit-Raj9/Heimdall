import "./experimentsI18n";

import { filterExperiments } from "@/components/experiments/experimentList";
import {
	experimentKind,
	humanExperimentName,
	isDebugExperiment,
	isHiddenByDefault,
	suiteName,
	systemExperiment,
	type ExperimentKind,
} from "@/components/experiments/experiments.utils";
import { useDriftMonitorNames } from "@/components/experiments/useDriftMonitorNames";
import { useEvaluationNames } from "@/components/experiments/useEvaluationNames";
import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { FilterSelect, FilterTokens, ShowTestsChip, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { Badge, type BadgeTone } from "@/components/ui/badge";
import { Pager, pageSlice } from "@/components/ui/pager";
import { StatusPill } from "@/components/ui/statusPill";
import { ValueCell } from "@/components/valueCell";
import { isTestObject, withoutTests } from "@/lib/testObjects";
import { getLocale } from "@/lib/i18n";
import type { ExperimentState, ExperimentStatus } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import {
	Brain,
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
} from "lucide-react";
import { Link } from "@tanstack/react-router";
import { parseAsString, useQueryStates } from "nuqs";
import { useEffect, useMemo, useState } from "react";
import "@/app/leaderboard/views/uf4bI18n";
import { cheaperArm } from "@/components/verdict/armCost";
import { useListExperimentsQuery } from "@/lib/store/apis/benchApi";
import { archivedCountOf, archivedParam, isArchived } from "@/lib/store/apis/archiveApi";
import { ArchiveRowMenu } from "@/components/ui/archiveAction";
import { useBulkArchive } from "./bulkArchive";
import { ARCHIVED_ROW_CLASS, ArchivedPill, ShowArchivedChip, withoutArchived } from "@/components/ui/showArchivedChip";
import type { ArmStat, ExperimentRow } from "@/lib/types/serverFields";
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

// M4-76: verdict pills use the shared Badge tones; status pills are the shared StatusPill.
const BADGE_TONE: Record<Tone, BadgeTone> = {
	green: "success",
	red: "danger",
	amber: "warning",
	blue: "info",
	grey: "neutral",
};

/** Verdict words and color for a decision. Unknown values read as pending. Pure. */
export function verdictTone(decision: string | undefined): {
	key: "improved" | "regressed" | "inconclusive" | "invalid" | "score" | "leader" | "pending";
	tone: Tone;
} {
	switch (decision) {
		case "improved":
			return { key: "improved", tone: "green" };
		// Three or more setups: one leads (fix wave 3, N244).
		case "leader":
			return { key: "leader", tone: "green" };
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

/** The Result filter choices, in reading order. */
const RESULT_KEYS = ["improved", "regressed", "inconclusive", "invalid", "leader", "score", "pending"] as const;

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

/** "agent model vs agent model" from the setups, else the arm ids; empty when neither is known (M4-67). */
export function setupsLine(e: Pick<ExperimentStatus, "setups" | "arms">): string {
	if (e.setups && e.setups.length > 0) {
		const parts = e.setups.map((s) => [s.agent, s.model].filter(Boolean).join(" "));
		// Q-254: setups that share agent and model differ in something else (a fault): name each by its arm.
		const same = parts.length > 1 && parts.every((p) => p === parts[0]);
		return (same ? e.setups.map((s, i) => `${parts[i]} (${s.arm})`) : parts).join(" vs ");
	}
	return e.arms && e.arms.length > 0 ? e.arms.join(" vs ") : "";
}

/** True when the row matches the Agent, Model and Result filters; an empty filter matches all. Pure. */
export function setupMatches(
	e: Pick<ExperimentStatus, "setups" | "decision">,
	f: { agent?: string | null; model?: string | null; result?: string | null },
): boolean {
	if (f.agent && !(e.setups ?? []).some((s) => s.agent === f.agent)) return false;
	if (f.model && !(e.setups ?? []).some((s) => s.model === f.model)) return false;
	if (f.result && verdictTone(e.decision).key !== f.result) return false;
	return true;
}

/** True when an experiment state matches a ?state= value: a status choice's states, else that exact state. Pure. */
export function stateMatches(state: string, filter: string | null | undefined): boolean {
	if (!filter) return true;
	const choice = STATUSES.find((c) => c.states[0] === filter);
	return choice ? choice.states.includes(state as ExperimentState) : state === filter;
}

const ARM_STATS = { arm_stats: true } as const;

/** True when a row is still running or analyzing, so the list is worth polling. Pure. */
export function listHasRunning(rows: readonly Pick<ExperimentStatus, "state">[] | undefined): boolean {
	return (rows ?? []).some((e) => e.state === "running" || e.state === "analyzing");
}

/**
 * The cost news of a row with no score winner (M4-66): which setup is 2 times or more cheaper per
 * run and whether both solved the same share, from the optional `arm_stats`. Uses the one cost
 * rule of the verdict page (`cheaperArm`); null when a cost is missing or the gap is small. Pure.
 */
export function listCostNews(
	arms: readonly string[],
	stats: readonly ArmStat[] | undefined,
): { arm: string; ratio: number; sameScore: boolean } | null {
	if (!stats || arms.length !== 2) return null;
	const by = new Map(stats.map((s) => [s.arm, s]));
	const costs: Record<string, number | null> = {};
	for (const a of arms) {
		const s = by.get(a);
		costs[a] = s && s.cost_usd != null && s.cost_runs > 0 ? s.cost_usd / s.cost_runs : null;
	}
	const cheap = cheaperArm(arms, costs);
	if (!cheap) return null;
	const [ra, rb] = arms.map((a) => by.get(a)?.pass_rate);
	return { ...cheap, sameScore: ra != null && rb != null && ra === rb };
}

function VerdictPill({
	decision,
	testId,
	arms = [],
	stats,
}: {
	decision: string | undefined;
	testId?: string;
	arms?: readonly string[];
	stats?: readonly ArmStat[];
}) {
	const { t } = useTranslation("experimentsUx");
	const { t: tp } = useTranslation("experimentPage");
	const v = verdictTone(decision);
	if (v.key === "pending") return <span className="text-muted-foreground text-xs">{t("verdict.pending")}</span>;
	// M4-66: no winner is not an alert: plain muted text, pills only for a result to act on. A much
	// cheaper setup is the news then, in the same sentence as the verdict page.
	if (v.key === "inconclusive") {
		const news = listCostNews(arms, stats);
		return (
			<span className="text-muted-foreground text-sm" data-testid={testId}>
				{news
					? tp(news.sameScore ? "summary.headline.sameScoreCheaper" : "summary.headline.unclearScoreCheaper", {
							setup: news.arm,
							ratio: new Intl.NumberFormat(getLocale(), { maximumSignificantDigits: 2 }).format(news.ratio),
						})
					: t("verdict.inconclusive")}
			</span>
		);
	}
	return (
		<Badge tone={BADGE_TONE[v.tone]} data-testid={testId}>
			{t(`verdict.${v.key}`)}
		</Badge>
	);
}

/** What a check or debug run is, in place of a verdict: none of them compares two setups (N253, N247). */
function KindPill({ kind, testId }: { kind: ExperimentKind; testId?: string }) {
	const { t } = useTranslation("experimentsUx");
	return (
		<span
			className="bg-muted text-muted-foreground inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap"
			data-testid={testId}
			data-kind={`kind-${kind}`}
		>
			{t(`kind.${kind}`)}
		</span>
	);
}

function RunsProgress({ e }: { e: ExperimentStatus }) {
	const { t } = useTranslation("experimentsUx");
	const { t: tu } = useTranslation("uf4b");
	const done = e.slots.done + e.slots.failed;
	const total = e.slots.total;
	const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
	return (
		<span className="flex min-w-24 flex-col items-end gap-1" aria-label={t("runsAria", { done, total })}>
			<span className="text-xs tabular-nums">
				<span className="md:hidden">{tu("list.runsMobile", { done, total })}</span>
				<span className="hidden md:inline">{t("runs", { done, total })}</span>
			</span>
			<span className="bg-muted hidden h-1 w-full overflow-hidden rounded-full sm:block">
				<span
					className={cn("block h-full rounded-full", e.slots.failed > 0 ? "bg-chart-warning" : "bg-primary")}
					style={{ width: `${pct}%` }}
				/>
			</span>
		</span>
	);
}

/** /experiments body: one toolbar (search, status, benchmark), and one row per experiment with a readable name. */
/** A list row with the optional archive field (phase D decision 6). */
type ArchivedRow = { archived_at?: string | null };

export function ExperimentBoard() {
	const { t } = useTranslation("experimentsUx");
	const { t: tb } = useTranslation("bench");
	const { t: tu } = useTranslation("uf4b");
	const locale = getLocale();
	// G3: ?arm_stats=1 adds per-setup numbers; a server without it answers the old list.
	// M4-79, M1-62: refresh every 15 s while a row runs, never when nothing does.
	const [anyRunning, setAnyRunning] = useState(false);
	const [filters, setFilters] = useQueryStates(
		{
			q: parseAsString,
			state: parseAsString,
			suite: parseAsString,
			all: parseAsString,
			agent: parseAsString,
			model: parseAsString,
			result: parseAsString,
			tests: parseAsString,
			page: parseAsString,
			archived: parseAsString,
		},
		{ history: "replace", throttleMs: 200 },
	);
	// Phase D decision 6: archived comparisons are hidden unless ?archived=1.
	const showArchived = filters.archived === "1";
	const listArgs = useMemo(() => ({ ...ARM_STATS, ...archivedParam(showArchived) }), [showArchived]);
	const list = useListExperimentsQuery(listArgs, { pollingInterval: anyRunning ? 15_000 : 0, skipPollingIfUnfocused: true });
	const runningNow = listHasRunning(list.data?.experiments);
	useEffect(() => setAnyRunning(runningNow), [runningNow]);
	// M1-18: comparisons the project's own checks made ("zz-test-...") hide behind "Show test items (n)".
	const showTests = filters.tests === "1";
	const { shown: everything, hidden: hiddenTests } = useMemo(
		() =>
			withoutTests(
				withoutArchived(list.data?.experiments ?? [], (e) => isArchived(e as ArchivedRow), showArchived).shown,
				(e) => isTestObject(e.exp_id) || isTestObject(e.name),
				showTests,
			),
		[list.data, showTests, showArchived],
	);
	const archivedSeen = useMemo(() => (list.data?.experiments ?? []).filter((e) => isArchived(e as ArchivedRow)).length, [list.data]);
	const archivedCount = archivedCountOf(list.data) ?? (showArchived ? archivedSeen : undefined);
	// Automatic checks, practice runs and the single-setup benchmarks of an evaluation are not
	// comparisons: hidden unless asked for (URL all=1), so the list matches its subtitle.
	const showSystem = filters.all === "1";
	const systemCount = useMemo(() => everything.filter(isHiddenByDefault).length, [everything]);
	const all = useMemo(() => (showSystem ? everything : everything.filter((e) => !isHiddenByDefault(e))), [everything, showSystem]);
	const evalNames = useEvaluationNames();
	const monitors = useDriftMonitorNames();
	// Every row is named, hidden ones too, so a search can say it matched rows that are hidden.
	const named = useMemo(
		() =>
			new Map(
				everything.map(
					(e) => [e.exp_id, humanExperimentName(e, (k, o) => String(t(k as never, (o ?? {}) as never)), evalNames, monitors)] as const,
				),
			),
		[everything, t, evalNames, monitors],
	);
	const query = filters.q?.trim().toLowerCase() ?? "";
	const matchesQuery = useMemo(
		() => (e: ExperimentStatus) =>
			e.exp_id.toLowerCase().includes(query) ||
			e.name.toLowerCase().includes(query) ||
			(named.get(e.exp_id) ?? "").toLowerCase().includes(query) ||
			(e.suite ?? "").toLowerCase().includes(query),
		[query, named],
	);
	// The suite filter applies first, so each status choice can say how many it would show.
	const inSuite = useMemo(() => filterExperiments(all, { suite: filters.suite }), [all, filters.suite]);
	const statusCounts = useMemo(
		() => new Map(STATUSES.map((c) => [c.key, inSuite.filter((e) => c.states.includes(e.state)).length] as const)),
		[inSuite],
	);
	const shown = useMemo(() => {
		// The id and typed name match in filterExperiments; the readable name and the suite match here.
		const base = inSuite.filter(
			(e) =>
				stateMatches(e.state, filters.state) && setupMatches(e, { agent: filters.agent, model: filters.model, result: filters.result }),
		);
		return query ? base.filter(matchesQuery) : base;
	}, [inSuite, filters.state, filters.agent, filters.model, filters.result, query, matchesQuery]);
	// A search that finds only hidden rows says so, with a button that shows them (N202).
	const hiddenMatches = useMemo(
		() => (!query || showSystem || shown.length > 0 ? 0 : everything.filter((e) => isHiddenByDefault(e) && matchesQuery(e)).length),
		[query, showSystem, shown.length, everything, matchesQuery],
	);
	const suites = useMemo(() => [...new Set(all.map((e) => e.suite).filter((s): s is string => Boolean(s)))].sort(), [all]);
	const agents = useMemo(() => [...new Set(all.flatMap((e) => (e.setups ?? []).map((s) => s.agent)).filter(Boolean))].sort(), [all]);
	const models = useMemo(() => [...new Set(all.flatMap((e) => (e.setups ?? []).map((s) => s.model)).filter(Boolean))].sort(), [all]);
	const clear = () => void setFilters({ q: null, state: null, suite: null, agent: null, model: null, result: null, page: null });
	// M1-59: 50 rows per page; any filter change goes back to page 1.
	const slice = pageSlice(shown, Number(filters.page) || 1);
	// Q-169: pick rows and archive them together.
	const bulk = useBulkArchive();
	const setFilter = (
		patch: Partial<Record<"q" | "state" | "suite" | "agent" | "model" | "result" | "all" | "tests" | "archived", string | null>>,
	) => void setFilters({ ...patch, page: null });
	const statusKey = STATUSES.find((c) => c.states[0] === filters.state)?.key;
	const tokens = (
		[
			filters.state && {
				key: "state",
				label: `${t("col.status")}: ${statusKey ? t(`chips.${statusKey}`) : filters.state}`,
				onRemove: () => setFilter({ state: null }),
			},
			filters.suite && {
				key: "suite",
				label: `${t("chips.benchmark")}: ${suiteName(filters.suite)}`,
				onRemove: () => setFilter({ suite: null }),
			},
			filters.agent && { key: "agent", label: `${tu("list.agent")}: ${filters.agent}`, onRemove: () => setFilter({ agent: null }) },
			filters.model && { key: "model", label: `${tu("list.model")}: ${filters.model}`, onRemove: () => setFilter({ model: null }) },
			filters.result && {
				key: "result",
				label: `${t("col.verdict")}: ${RESULT_KEYS.includes(filters.result as never) ? t(`verdict.${filters.result as (typeof RESULT_KEYS)[number]}`) : filters.result}`,
				onRemove: () => setFilter({ result: null }),
			},
		] as (FilterToken | false | "" | null | undefined)[]
	).filter((x): x is FilterToken => Boolean(x));

	if (list.isLoading) return <BlockSkeleton rows={13} testId="experiment-list-loading" />;
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
			<div className="flex flex-col items-center gap-2">
				<EmptyState
					icon={FlaskConical}
					message={tb("experiments.list.empty")}
					action={{ label: tu("list.compareVersions"), to: "/evaluate?mode=versions" }}
					testId="experiment-list-empty"
				/>
				<Link to="/quickstart" className="text-primary text-sm hover:underline" data-testid="experiment-list-empty-cli">
					{tu("list.useCommandLine")}
				</Link>
			</div>
		);
	}

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="experiment-list">
			<div className="flex flex-col gap-2" role="search" aria-label={tb("experiments.list.filtersLabel")}>
				<div className="flex flex-wrap items-center gap-2" role="toolbar" data-testid="experiment-toolbar">
					<ToolbarSearch
						value={filters.q ?? ""}
						onChange={(v) => setFilter({ q: v || null })}
						placeholder={t("searchPlaceholder")}
						label={tb("experiments.list.search")}
						testId="experiment-search"
					/>
					<FilterSelect
						label={t("col.status")}
						allLabel={t("chips.all")}
						options={STATUSES.map((c) => ({ value: c.states[0], label: `${t(`chips.${c.key}`)} (${statusCounts.get(c.key) ?? 0})` }))}
						value={filters.state}
						onChange={(v) => setFilter({ state: v })}
						testId="experiment-filter-status"
					/>
					{(suites.length > 1 || filters.suite) && (
						<FilterSelect
							label={t("chips.benchmark")}
							allLabel={t("chips.all")}
							options={suites.map((s) => ({ value: s, label: suiteName(s) }))}
							value={filters.suite}
							onChange={(v) => setFilter({ suite: v })}
							testId="experiment-filter-suite"
						/>
					)}
					{(agents.length > 1 || filters.agent) && (
						<FilterSelect
							label={tu("list.agent")}
							allLabel={t("chips.all")}
							options={agents.map((a) => ({ value: a, label: a }))}
							value={filters.agent}
							onChange={(v) => setFilter({ agent: v })}
							testId="experiment-filter-agent"
						/>
					)}
					{(models.length > 1 || filters.model) && (
						<FilterSelect
							label={tu("list.model")}
							allLabel={t("chips.all")}
							options={models.map((m) => ({ value: m, label: m }))}
							value={filters.model}
							onChange={(v) => setFilter({ model: v })}
							testId="experiment-filter-model"
						/>
					)}
					<FilterSelect
						label={t("col.verdict")}
						allLabel={t("chips.all")}
						options={RESULT_KEYS.map((k) => ({ value: k, label: t(`verdict.${k}`) }))}
						value={filters.result}
						onChange={(v) => setFilter({ result: v })}
						testId="experiment-filter-result"
					/>
					<ShowTestsChip
						count={hiddenTests}
						on={showTests}
						onChange={(on) => setFilter({ tests: on ? "1" : null })}
						testId="experiment-show-tests"
					/>
					<ShowArchivedChip
						count={archivedCount}
						on={showArchived}
						onChange={(on) => setFilter({ archived: on ? "1" : null })}
						testId="experiment-show-archived"
					/>
					{systemCount > 0 && (
						<label className="text-muted-foreground ml-auto inline-flex min-h-6 items-center gap-2 text-sm" title={t("system.toggleHint")}>
							{/* M4-75: a switch, not a 14 px checkbox. */}
							<input
								type="checkbox"
								role="switch"
								aria-checked={showSystem}
								checked={showSystem}
								onChange={(ev) => setFilter({ all: ev.target.checked ? "1" : null })}
								className="bg-muted checked:bg-primary before:bg-background relative h-5 w-9 shrink-0 cursor-pointer appearance-none rounded-full border transition-colors before:absolute before:top-0.5 before:left-0.5 before:size-3.5 before:rounded-full before:shadow-sm before:transition-transform checked:before:translate-x-4"
								data-testid="experiment-show-system"
							/>
							{t("system.toggle", { count: systemCount })}
						</label>
					)}
				</div>
				<FilterTokens tokens={tokens} onClear={clear} testId="experiment-tokens" />
			</div>

			{hiddenMatches > 0 && (
				<p
					className="text-muted-foreground flex flex-wrap items-center gap-2 text-sm"
					role="status"
					data-testid="experiment-hidden-matches"
				>
					{t("hiddenMatches", { count: hiddenMatches })}
					<button
						type="button"
						className="text-primary font-medium hover:underline"
						onClick={() => setFilter({ all: "1" })}
						data-testid="experiment-show-hidden-matches"
					>
						{t("showHiddenMatches")}
					</button>
				</p>
			)}
			{shown.length === 0 ? (
				<EmptyState
					icon={Search}
					message={tb("experiments.list.noMatch")}
					action={{ label: tb("runs.list.clear"), onClick: clear }}
					compact
					testId="experiment-list-no-match"
				/>
			) : (
				<>
					{bulk.bar}
					<div className="bg-card overflow-hidden rounded-xl border">
						<div
							className="text-muted-foreground bg-muted/40 hidden grid-cols-[minmax(0,1fr)_8rem_10rem_7rem_6rem_7rem_2.5rem] items-center gap-4 border-b px-5 py-2.5 text-xs font-medium md:grid"
							aria-hidden="true"
						>
							<span>{t("col.experiment")}</span>
							<span>{t("col.status")}</span>
							<span>{t("col.verdict")}</span>
							<span className="text-right">{t("col.runs")}</span>
							<span className="text-right">{t("col.cost")}</span>
							<span className="text-right">{t("col.when")}</span>
							<span />
						</div>
						<ul className="divide-y" data-testid="experiment-table" aria-label={tb("experiments.list.tableLabel")}>
							{slice.rows.map((e) => {
								const when = e.started_at ?? e.frozen_at;
								const title = named.get(e.exp_id) ?? e.exp_id;
								const kind = experimentKind(e);
								const archivedAt = (e as ArchivedRow).archived_at;
								return (
									<li
										key={e.exp_id}
										className={`${isArchived(e as ArchivedRow) ? ARCHIVED_ROW_CLASS : ""} hover:bg-muted/40 relative flex flex-col gap-3 px-5 py-4 transition-colors md:grid md:grid-cols-[minmax(0,1fr)_8rem_10rem_7rem_6rem_7rem_2.5rem] md:items-center md:gap-4`}
										data-testid={`experiment-row-${e.exp_id}`}
									>
										<span className="flex min-w-0 items-center gap-3">
											{bulk.box(e.exp_id, title)}
											<span className="flex min-w-0 flex-col">
												<EntityLink
													kind="experiment"
													id={e.exp_id}
													className="text-foreground truncate font-sans text-sm font-semibold after:absolute after:inset-0"
													testId={`experiment-row-${e.exp_id}-name`}
												>
													<span title={title}>{title}</span>
												</EntityLink>
												{isArchived(e as ArchivedRow) && (
													<span className="mt-1">
														<ArchivedPill at={archivedAt} testId={`experiment-row-${e.exp_id}-archived`} />
													</span>
												)}
												<span className="text-muted-foreground truncate text-xs">
													{suiteName(e.suite)}
													{systemExperiment(e) ? (
														<span> · {t("system.tag")}</span>
													) : isDebugExperiment(e) ? (
														<span> · {t("debug.tag")}</span>
													) : (
														setupsLine(e) && <span data-testid={`experiment-row-${e.exp_id}-setups`}> · {setupsLine(e)}</span>
													)}
												</span>
											</span>
										</span>
										<span className="flex flex-wrap items-center gap-2 md:contents">
											<span>
												{/* The results index counts only scored runs, so a debug run reads "created, 0 runs" there (N247): its kind says enough. */}
												{!(kind === "debug" && e.state === "created") && (
													<StatusPill raw={e.state} testId={`experiment-row-${e.exp_id}-state`} />
												)}
											</span>
											<span>
												{kind ? (
													<KindPill kind={kind} testId={`experiment-row-${e.exp_id}-kind`} />
												) : (
													// A running comparison has no result yet: an analysis of its first runs is not final (N197).
													<VerdictPill
														decision={e.state === "running" || e.state === "analyzing" ? undefined : e.decision}
														testId={`experiment-row-${e.exp_id}-decision`}
														arms={e.arms}
														stats={(e as ExperimentRow).arm_stats}
													/>
												)}
											</span>
											<span className="ml-auto md:ml-0 md:flex md:justify-end">{kind !== "debug" && <RunsProgress e={e} />}</span>
											<span className="text-right text-sm">
												{e.spent_usd?.state === "observed" ? (
													<>
														<span className="text-muted-foreground mr-1 text-xs md:hidden">{tu("list.cost")}</span>
														<ValueCell value={e.spent_usd} kind="usd" />
													</>
												) : (
													// M4-71: an unknown cost is words with a pointer to the price list, not a hatched stripe.
													<span
														className="text-muted-foreground text-xs"
														title={tu("list.notPricedHint")}
														data-testid={`experiment-row-${e.exp_id}-not-priced`}
													>
														{tu("list.notPriced")}
													</span>
												)}
											</span>
											<span
												className="text-muted-foreground text-right text-xs whitespace-nowrap tabular-nums"
												title={formatWhen(when, locale) ?? undefined}
											>
												{formatRelative(when, locale) ?? ""}
											</span>
											<span className="md:flex md:justify-end">
												<ArchiveRowMenu
													kind="experiment"
													id={e.exp_id}
													name={title}
													archivedAt={archivedAt}
													testId={`experiment-row-${e.exp_id}`}
												/>
											</span>
										</span>
									</li>
								);
							})}
						</ul>
					</div>
				</>
			)}
			<p className="text-muted-foreground text-xs tabular-nums" data-testid="experiment-list-count">
				{t("count", { shown: shown.length, total: all.length })}
				{!showSystem && systemCount > 0 && ` ${t("system.hidden", { count: systemCount })}`}
			</p>
		</div>
	);
}