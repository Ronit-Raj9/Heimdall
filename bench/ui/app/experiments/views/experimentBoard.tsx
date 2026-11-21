import "./experimentsI18n";

import { filterExperiments } from "@/components/experiments/experimentList";
import { systemExperiment } from "@/components/experiments/experiments.utils";
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
import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

/** A name the server wrote for the user (QuickRun) rather than one the user typed. */
const AUTO_NAME = /^quick-\d{8}-\d{6}(-[0-9a-f]+)?$/i;

/** Suite id without its version: coding-core@1.0.0 to coding-core. */
export function suiteName(suite: string | undefined): string {
	return (suite ?? "").split("@")[0];
}

/** True when the experiment's name was generated (or missing), so the UI should build a readable one. */
export function isAutoName(e: Pick<ExperimentStatus, "name" | "exp_id">): boolean {
	const name = e.name.trim();
	return !name || name === e.exp_id || AUTO_NAME.test(name);
}

/**
 * The label shown for an experiment. A typed name wins. A generated one becomes
 * "<arm> vs <arm> on <suite>" when the arm ids say something, else "Quick run on <suite>". Pure.
 */
export function humanExperimentName(
	e: Pick<ExperimentStatus, "name" | "exp_id" | "suite" | "arms">,
	t: (key: "quickRun" | "armsVs" | "noSuite" | "system.readiness" | "system.agent" | "system.drift", opts?: Record<string, string>) => string,
): string {
	// Heimdall's own checks (admit-*, check-*, canary-*) read as what they are, not as comparisons.
	const sys = systemExperiment(e);
	if (sys) return t(`system.${sys.kind}`, { name: sys.subject });
	if (!isAutoName(e)) return e.name.trim();
	const suite = suiteName(e.suite) || t("noSuite");
	const arms = (e.arms ?? []).filter(Boolean);
	// Single-letter ids (A, B) carry no meaning on their own.
	if (arms.length > 0 && arms.some((a) => a.length > 1)) return t("armsVs", { arms: arms.join(" vs "), suite });
	return t("quickRun", { suite });
}

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
	key: "improved" | "regressed" | "inconclusive" | "invalid" | "pending";
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

/** Status choices: each maps to one state filter in the URL. */
const STATUSES: readonly {
	key: "active" | "done" | "failed" | "created";
	state: string;
}[] = [
	{ key: "active", state: "running" },
	{ key: "done", state: "done" },
	{ key: "failed", state: "failed" },
	{ key: "created", state: "created" },
];

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
		{ q: parseAsString, state: parseAsString, suite: parseAsString },
		{ history: "replace", throttleMs: 200 },
	);
	const everything = useMemo(() => list.data?.experiments ?? [], [list.data]);
	// Automatic readiness, agent and drift checks are hidden unless asked for.
	const [showSystem, setShowSystem] = useState(false);
	const systemCount = useMemo(() => everything.filter((e) => systemExperiment(e)).length, [everything]);
	const all = useMemo(() => (showSystem ? everything : everything.filter((e) => !systemExperiment(e))), [everything, showSystem]);
	const named = useMemo(() => new Map(all.map((e) => [e.exp_id, humanExperimentName(e, (k, o) => String(t(k as never, (o ?? {}) as never)))] as const)), [all, t]);
	const shown = useMemo(() => {
		const q = filters.q?.trim().toLowerCase() ?? "";
		// The id and typed name match in filterExperiments; the readable name and the suite match here.
		const base = filterExperiments(all, {
			state: filters.state,
			suite: filters.suite,
		});
		return q
			? base.filter(
					(e) =>
						e.exp_id.toLowerCase().includes(q) ||
						e.name.toLowerCase().includes(q) ||
						(named.get(e.exp_id) ?? "").toLowerCase().includes(q) ||
						(e.suite ?? "").toLowerCase().includes(q),
				)
			: base;
	}, [all, filters, named]);
	const suites = useMemo(() => [...new Set(all.map((e) => e.suite).filter((s): s is string => Boolean(s)))].sort(), [all]);
	const clear = () => void setFilters({ q: null, state: null, suite: null });
	const statusKey = STATUSES.find((c) => c.state === filters.state)?.key;
	const tokens: FilterToken[] = [
		filters.state && {
			key: "state",
			label: `${t("col.status")}: ${statusKey ? t(`chips.${statusKey}`) : filters.state}`,
			onRemove: () => void setFilters({ state: null }),
		},
		filters.suite && { key: "suite", label: `${t("chips.benchmark")}: ${suiteName(filters.suite)}`, onRemove: () => void setFilters({ suite: null }) },
	].filter((x): x is FilterToken => Boolean(x));

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
						options={STATUSES.map((c) => ({ value: c.state, label: t(`chips.${c.key}`) }))}
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
								onChange={(ev) => setShowSystem(ev.target.checked)}
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