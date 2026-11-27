import { faultName } from "@/components/reportcard/reportCard.utils";
import { EmptyState } from "@/components/shell/emptyState";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { outcomeTone } from "@/components/runs/runs.utils";
import { Button } from "@/components/ui/button";
import { Pager, pageWindow } from "@/components/ui/pager";
import { StatusDot } from "@/components/ui/statusPill";
import { FilterSelect, FilterTokens, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import { useGetAgentsQuery, useListRunsQuery } from "@/lib/store/apis/benchApi";
import { archivedCountOf, isArchived } from "@/lib/store/apis/archiveApi";
import { ArchiveRowMenu } from "@/components/ui/archiveAction";
import { ARCHIVED_ROW_CLASS, ArchivedPill, ShowArchivedChip } from "@/components/ui/showArchivedChip";
import type { RunListParams, RunListRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link, useNavigate } from "@tanstack/react-router";
import { suiteDisplayName } from "@/components/suites/suiteDisplay";
import "@/components/runs/uf5bI18n";
import { ArrowDown, ArrowUp, ArrowUpDown, ListX, SlidersHorizontal } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useState, type MouseEvent } from "react";
import { TraceMatches, TraceSearchInput } from "./traceMatches";
import { pastLastPage } from "@/components/runs/runList";
import { saveRunsSearch } from "@/components/runs/runsListSearch";
import "./w4m4I18n";

export { pastLastPage };
import { useTranslation } from "react-i18next";
import "./fw21I18n";

export const RUNS_PAGE = 50;

/** While any run on the page is still running the list asks again this often (X-84). */
export const RUNS_POLL_MS = 15_000;

const parsers = {
	outcome: parseAsString,
	suite: parseAsString,
	/** Older links and other pages say ?benchmark=: read as an alias of suite and rewritten (M5-40). */
	benchmark: parseAsString,
	arm: parseAsString,
	agent: parseAsString,
	fault: parseAsString,
	q: parseAsString,
	tq: parseAsString,
	page: parseAsInteger,
	sort: parseAsString,
	dir: parseAsString,
	/** Phase D decision 6: archived runs are hidden unless ?archived=1. */
	archived: parseAsString,
};

export type RunSortKey = NonNullable<RunListParams["sort"]>;
export type RunSortDir = NonNullable<RunListParams["dir"]>;

/** The sort keys GET /runs accepts. */
export const RUN_SORT_KEYS: readonly RunSortKey[] = ["started", "duration", "outcome", "cost", "tokens", "steps", "task", "agent"];

/** Keys whose first click sorts A to Z; every other key starts with the largest or newest first. */
const TEXT_SORTS: readonly RunSortKey[] = ["task", "agent", "outcome"];

/** The sort in the URL, or null when it is missing or not one the server accepts. */
export function parseRunSort(
	sort: string | null | undefined,
	dir: string | null | undefined,
): { sort: RunSortKey; dir: RunSortDir } | null {
	if (!sort || !(RUN_SORT_KEYS as readonly string[]).includes(sort)) return null;
	const d: RunSortDir = dir === "asc" || dir === "desc" ? dir : TEXT_SORTS.includes(sort as RunSortKey) ? "asc" : "desc";
	return { sort: sort as RunSortKey, dir: d };
}

/** The URL state after a click on the header of key: the same column flips its direction, another column starts in its natural one. The default order (newest first) clears both. */
export function nextRunSort(
	current: { sort?: string | null; dir?: string | null },
	key: RunSortKey,
): { sort: RunSortKey | null; dir: RunSortDir | null } {
	const cur = parseRunSort(current.sort, current.dir) ?? { sort: "started" as RunSortKey, dir: "desc" as RunSortDir };
	const dir: RunSortDir = cur.sort === key ? (cur.dir === "asc" ? "desc" : "asc") : TEXT_SORTS.includes(key) ? "asc" : "desc";
	if (key === "started" && dir === "desc") return { sort: null, dir: null };
	return { sort: key, dir };
}

/** The aria-sort value of a column header. */
export function ariaSortOf(current: { sort?: string | null; dir?: string | null }, key: RunSortKey): "ascending" | "descending" | "none" {
	const cur = parseRunSort(current.sort, current.dir) ?? { sort: "started", dir: "desc" };
	if (cur.sort !== key) return "none";
	return cur.dir === "asc" ? "ascending" : "descending";
}

/** The server request for the filters, sort and page; agent, fault and the sort apply on the server across every page, the search box filters the page on the client. */
export function runsRequest(
	f: {
		outcome?: string | null;
		suite?: string | null;
		arm?: string | null;
		agent?: string | null;
		fault?: string | null;
		sort?: string | null;
		dir?: string | null;
		archived?: string | null;
	},
	page: number | null,
): RunListParams {
	const params: RunListParams = { limit: RUNS_PAGE };
	const sort = parseRunSort(f.sort, f.dir);
	if (sort) {
		params.sort = sort.sort;
		params.dir = sort.dir;
	}
	if (f.outcome) params.outcome = f.outcome;
	if (f.suite) params.suite = f.suite;
	if (f.arm) params.arm = f.arm;
	if (f.agent) params.agent = f.agent;
	if (f.fault) params.fault = f.fault;
	if (f.archived === "1") (params as RunListParams & { archived?: "include" }).archived = "include";
	const p = Math.max(1, page ?? 1);
	if (p > 1) params.offset = (p - 1) * RUNS_PAGE;
	return params;
}

/** The agent a run used: its agent config id, or the arm id when the run has none. */
export function runAgent(r: Pick<RunListRow, "config_id" | "arm">): string {
	return r.config_id || r.arm;
}

/** Agent names for the Agent filter: every configured agent, every agent the server saw in any run, and any agent on the page. Never arm ids. */
export function agentOptions(
	configured: readonly string[],
	rows: readonly Pick<RunListRow, "config_id">[],
	seenOnServer: readonly string[] = [],
): string[] {
	const seen = new Set(configured.filter(Boolean));
	for (const r of rows) if (r.config_id) seen.add(r.config_id);
	for (const a of seenOnServer) if (a) seen.add(a);
	return [...seen].sort((a, b) => a.localeCompare(b));
}

/** Running runs first, then the server's newest-first order; with a chosen sort the server's order is kept as is. A search narrows by task, agent or model, the agent filter by agent name. */
export function orderRuns(rows: readonly RunListRow[], query: string | null, agent: string | null = null, sorted = false): RunListRow[] {
	const q = (query ?? "").trim().toLowerCase();
	const hit = (r: RunListRow) => !q || [r.task, r.arm, r.config_id ?? "", r.model ?? "", r.suite].some((v) => v.toLowerCase().includes(q));
	const shown = rows.filter((r) => hit(r) && (!agent || runAgent(r) === agent));
	if (sorted) return shown;
	return [...shown.filter((r) => !r.sealed), ...shown.filter((r) => r.sealed)];
}

/** The agent version shown next to its name: the registered version (v2), else the agent's own version string. */
export function agentVersionLabel(r: Pick<RunListRow, "version" | "agent_version">): string {
	if (r.version && r.version > 0) return `v${r.version}`;
	return r.agent_version ?? "";
}

function agentTitle(r: RunListRow): string {
	return [runAgent(r), agentVersionLabel(r), r.model].filter(Boolean).join(" · ");
}

/** Seconds between start and seal, or null while either is missing. */
/**
 * A run's fault as the run list names it (N291): the fault catalog's name, the same one the report
 * cards use, else the id in sentence case. The product prefix of a probe id is left out.
 */
export function runFaultLabel(fault: string): string {
	const name = faultName(fault.replace(/^heimdall-/, ""));
	return name.charAt(0).toUpperCase() + name.slice(1);
}

/** The URL after reading ?benchmark= as the suite filter: an explicit suite wins, benchmark is dropped (M5-40). */
export function benchmarkAlias(s: { suite?: string | null; benchmark?: string | null }): { suite: string | null; benchmark: null } {
	return { suite: s.suite || s.benchmark || null, benchmark: null };
}

export function runDuration(row: Pick<RunListRow, "started" | "sealed_at">): number | null {
	if (!row.started || !row.sealed_at) return null;
	const s = (new Date(row.sealed_at).getTime() - new Date(row.started).getTime()) / 1000;
	return Number.isFinite(s) && s >= 0 ? s : null;
}

export function formatDuration(seconds: number | null): string {
	if (seconds === null) return "-";
	if (seconds < 60) return `${Math.round(seconds)}s`;
	const m = Math.floor(seconds / 60);
	if (m < 60) return `${m}m ${Math.round(seconds % 60)}s`;
	return `${Math.floor(m / 60)}h ${m % 60}m`;
}

export function timeAgo(iso: string | undefined, locale: string, now = Date.now()): string {
	if (!iso) return "";
	const at = new Date(iso).getTime();
	if (Number.isNaN(at)) return "";
	const diff = Math.round((at - now) / 1000);
	const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" });
	const abs = Math.abs(diff);
	if (abs < 60) return rtf.format(diff, "second");
	if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
	if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
	return rtf.format(Math.round(diff / 86400), "day");
}

function formatNumber(n: number | undefined, locale: string): string {
	if (n === undefined || n === null) return "-";
	return new Intl.NumberFormat(locale, { notation: n >= 10000 ? "compact" : "standard", maximumFractionDigits: 1 }).format(n);
}

/** Reference agents that call no model: their tokens and cost are not measured, not zero (M5-31). */
const BASELINE_AGENTS = /^(oracle|noop)(?:$|[^a-z0-9])/i;

/** True for a run of a reference agent (oracle, noop) that calls no model. */
export function isBaselineRun(r: Pick<RunListRow, "config_id" | "arm">): boolean {
	return BASELINE_AGENTS.test(runAgent(r));
}

/** Outcomes where the run itself does not count: the grader or the infrastructure broke (M5-38). */
const WARN_OUTCOMES: readonly string[] = ["invalid_evaluator", "infra_error", "aborted_infra"];

export type RunTone = "pass" | "zero" | "unknown" | "live" | "warn";

/** The list's tone of a run: running, a broken grader or infrastructure (warn), else the outcome's tone. */
export function runTone(row: Pick<RunListRow, "sealed" | "outcome">): RunTone {
	if (!row.sealed) return "live";
	if (WARN_OUTCOMES.includes(row.outcome)) return "warn";
	return outcomeTone(row.outcome);
}

// The shared status pill, with the run list's own outcome words (M5-63).
const PILL_TONE: Record<RunTone, "ok" | "bad" | "neutral" | "running" | "warn"> = {
	pass: "ok",
	zero: "bad",
	unknown: "neutral",
	live: "running",
	warn: "warn",
};

/** Outcome as a status dot and its word (X-37): a quiet list, no filled pill; a running run's dot pulses. */
export function OutcomePill({ row }: { row: Pick<RunListRow, "sealed" | "outcome" | "run_id"> }) {
	const { t: tu } = useTranslation("u12");
	const { t: tb } = useTranslation("bench");
	const { t: t5 } = useTranslation("uf5b");
	const tone = runTone(row);
	return (
		<span
			data-slot="status-dot-text"
			data-tone={PILL_TONE[tone]}
			className="text-foreground inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap"
			title={tone === "warn" ? t5("runs.graderBroken") : undefined}
			data-testid={`runs-outcome-${row.run_id}`}
			data-outcome={row.sealed ? row.outcome : "running"}
			data-run-tone={tone}
		>
			<StatusDot tone={PILL_TONE[tone]} />
			{row.sealed ? tb(`runs.outcome.${row.outcome}`, { defaultValue: row.outcome }) : tu("runs.live")}
		</span>
	);
}

/** Server fields the runs list may send (G1); read as optional, older servers omit them. */
export type RunListRowNamed = RunListRow & { task_title?: string; suite_title?: string; exp_id?: string; exp_name?: string };

/** The task's readable title when the server sends one, else its id. */
export function runTaskLabel(row: RunListRowNamed): string {
	return row.task_title?.trim() || row.task;
}

/** The benchmark line: the server's title, else the known display name of the slug. */
export function runSuiteLabel(row: RunListRowNamed): string {
	return row.suite_title?.trim() || suiteDisplayName(row.suite.replace(/@.*$/, ""));
}

/** /runs: live runs on top, toolbar filters in the URL, a search box, and one click to open a run. */
/** A run row with the optional archive field (phase D decision 6). */
type ArchivedRun = { archived_at?: string | null };

/** The row's Archive or Unarchive menu; a running run is refused by the server, and its reason shows. */
function RunRowMenu({ row, testId }: { row: RunListRow; testId: string }) {
	return <ArchiveRowMenu kind="run" id={row.run_id} name={runTaskLabel(row)} archivedAt={(row as ArchivedRun).archived_at} testId={testId} />;
}

export function RunsTable() {
	const { t: ta } = useTranslation("ufd5");
	const { t: tu } = useTranslation("u12");
	const { t: tb } = useTranslation("bench");
	const { t: tw } = useTranslation("w4m4");
	const locale = getLocale();
	const navigate = useNavigate();
	const [search, setSearch] = useQueryStates(parsers, { history: "replace" });
	// Poll while a run on the page is still running, like the evaluations list (X-84).
	const [anyLive, setAnyLive] = useState(false);
	const list = useListRunsQuery(runsRequest(search, search.page), { pollingInterval: anyLive ? RUNS_POLL_MS : 0, skipPollingIfUnfocused: true });
	useEffect(() => setAnyLive((list.data?.runs ?? []).some((r) => !r.sealed)), [list.data]);
	useEffect(() => {
		if (search.benchmark) void setSearch(benchmarkAlias(search));
	}, [search.benchmark]); // eslint-disable-line react-hooks/exhaustive-deps
	// M5-7: remember the filters, so the run page crumb leads back to this same list.
	useEffect(() => {
		if (typeof window !== "undefined") saveRunsSearch(window.location.search);
	}, [search]);
	const { t: t5 } = useTranslation("uf5b");
	const [filtersOpen, setFiltersOpen] = useState(false);
	const agentsQuery = useGetAgentsQuery();
	const sorted = parseRunSort(search.sort, search.dir) !== null;
	const showArchived = search.archived === "1";
	const rows = useMemo(
		() =>
			orderRuns(
				(list.data?.runs ?? []).filter((r) => showArchived || !isArchived(r as ArchivedRun)),
				search.q,
				search.agent,
				sorted,
			),
		[list.data, search.q, search.agent, sorted, showArchived],
	);
	const archivedCount = archivedCountOf(list.data) ?? (showArchived ? (list.data?.runs ?? []).filter((r) => isArchived(r as ArchivedRun)).length : undefined);
	const onSort = (key: RunSortKey) => void setSearch({ ...nextRunSort(search, key), page: null });
	const agents = useMemo(
		() =>
			agentOptions(
				(agentsQuery.data?.agents ?? []).map((a) => a.id),
				list.data?.runs ?? [],
				list.data?.facets.agents ?? [],
			),
		[agentsQuery.data, list.data],
	);
	const live = rows.filter((r) => !r.sealed).length;
	const active = Boolean(search.outcome || search.suite || search.arm || search.agent || search.fault || search.q);
	const page = Math.max(1, search.page ?? 1);
	const facets = list.data?.facets;
	const clear = () => void setSearch({ outcome: null, suite: null, arm: null, agent: null, fault: null, q: null, tq: null, page: null });
	const setTraceQuery = useCallback((v: string) => void setSearch({ tq: v.trim() ? v : null }), [setSearch]);
	const traceQuery = (search.tq ?? "").trim();
	const outcomeLabel = (o: string) => tb(`runs.outcome.${o}`, { defaultValue: o });
	const faultLabel = runFaultLabel;
	const tokens = (
		[
			search.outcome && {
				key: "outcome",
				label: `${tu("runs.outcome")}: ${outcomeLabel(search.outcome)}`,
				onRemove: () => void setSearch({ outcome: null, page: null }),
			},
			search.suite && {
				key: "suite",
				label: `${tu("runs.suite")}: ${search.suite.replace(/@.*$/, "")}`,
				onRemove: () => void setSearch({ suite: null, page: null }),
			},
			search.agent && {
				key: "agent",
				label: `${tu("runs.agent")}: ${search.agent}`,
				onRemove: () => void setSearch({ agent: null, page: null }),
			},
			search.fault && {
				key: "fault",
				label: `${tu("runs.fault")}: ${faultLabel(search.fault)}`,
				onRemove: () => void setSearch({ fault: null, page: null }),
			},
			// An older link may still carry an arm id; show it so it can be removed.
			search.arm && { key: "arm", label: `${tu("runs.agent")}: ${search.arm}`, onRemove: () => void setSearch({ arm: null, page: null }) },
		] as (FilterToken | false | "" | null | undefined)[]
	).filter((x): x is FilterToken => Boolean(x));
	const open = (row: RunListRow) => void navigate({ to: "/runs/$runId", params: { runId: row.run_id } } as never);
	// The task title is the real link (keyboard, middle click, new tab); a mouse click anywhere else on the row opens it too.
	const onRowClick = (e: MouseEvent, row: RunListRow) => {
		if ((e.target as HTMLElement).closest("a,button,input,select")) return;
		open(row);
	};
	const filterCount = [search.outcome, search.suite, search.agent, search.fault, search.tq].filter(Boolean).length;
	const win = list.data ? pageWindow(list.data.total, page, RUNS_PAGE) : null;
	const pager =
		list.data && win ? (
			<Pager
				page={win.page}
				pageCount={win.pageCount}
				onPage={(n) => void setSearch({ page: n === 1 ? null : n })}
				rangeText={tu("runs.showing", { first: win.first, last: win.last, total: list.data.total })}
				testId="runs-pager"
			/>
		) : null;

	const sel = "h-10 w-full md:h-8 md:w-auto";
	const runLink = (row: RunListRow, className?: string) => (
		<Link
			to="/runs/$runId"
			params={{ runId: row.run_id }}
			className={cn("focus-visible:outline-ring rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2", className)}
			title={row.task}
			aria-label={t5("runs.openRun", { task: runTaskLabel(row) })}
			data-testid={`runs-link-${row.run_id}`}
		>
			{runTaskLabel(row)}
		</Link>
	);
	const tokensCell = (row: RunListRow) =>
		row.tokens === undefined || row.tokens === null || isBaselineRun(row) ? (
			<span title={tu("runs.notRecorded")} data-testid={`runs-tokens-unrecorded-${row.run_id}`}>
				-
			</span>
		) : (
			formatNumber(row.tokens, locale)
		);
	const costCell = (row: RunListRow) =>
		isBaselineRun(row) ? <span title={tu("runs.notRecorded")}>-</span> : <ValueCell value={row.cost_usd} kind="usd" focusable={false} />;

	return (
		<div className="flex min-w-0 flex-col gap-4" data-testid="runs-table-screen">
			<div className="flex flex-col gap-2">
				<div className="flex flex-wrap items-center gap-2" role="toolbar" aria-label={tu("runs.filters")} data-testid="runs-toolbar">
					<ToolbarSearch
						value={search.q ?? ""}
						onChange={(v) => void setSearch({ q: v || null })}
						placeholder={tu("runs.search")}
						label={tu("runs.searchLabel")}
						testId="runs-search"
					/>
					{/* Phones: one search, the other filters behind "Filters (n)" (M5-32). */}
					<Button
						variant="outline"
						size="sm"
						className="h-10 md:hidden"
						aria-expanded={filtersOpen}
						aria-controls="runs-filters"
						onClick={() => setFiltersOpen((o) => !o)}
						dataTestId="runs-filters-toggle"
					>
						<SlidersHorizontal aria-hidden="true" />
						{filterCount > 0 ? t5("runs.filtersCount", { count: filterCount }) : t5("runs.filters")}
					</Button>
					<div
						id="runs-filters"
						className={cn("w-full flex-col gap-2 md:flex md:w-auto md:flex-row md:flex-wrap md:items-center", filtersOpen ? "flex" : "hidden")}
						data-testid="runs-filters"
					>
						{/* The trace search hint shows while its field has focus (M5-39). */}
						<div className="group/tq flex w-full flex-col gap-1 md:w-auto">
							<TraceSearchInput value={search.tq ?? ""} onChange={setTraceQuery} />
							<span className="text-muted-foreground hidden text-xs group-focus-within/tq:block" data-testid="runs-trace-hint">
								{t5("runs.searchHint")}
							</span>
						</div>
						<FilterSelect
							label={tu("runs.outcome")}
							allLabel={tu("runs.all")}
							options={(facets?.outcomes ?? []).map((o) => ({ value: o, label: outcomeLabel(o) }))}
							value={search.outcome}
							onChange={(v) => void setSearch({ outcome: v, page: null })}
							className={sel}
							testId="runs-filter-outcome"
						/>
						<FilterSelect
							label={tu("runs.suite")}
							allLabel={tu("runs.all")}
							options={(facets?.suites ?? []).map((v) => ({ value: v, label: suiteDisplayName(v.replace(/@.*$/, "")) }))}
							value={search.suite}
							onChange={(v) => void setSearch({ suite: v, page: null })}
							className={sel}
							testId="runs-filter-suite"
						/>
						<FilterSelect
							label={tu("runs.agent")}
							allLabel={tu("runs.all")}
							options={agents.map((v) => ({ value: v, label: v }))}
							value={search.agent}
							onChange={(v) => void setSearch({ agent: v, page: null })}
							className={sel}
							testId="runs-filter-agent"
						/>
						{(facets?.faults ?? []).some((f) => f !== "clean") && (
							<FilterSelect
								label={tu("runs.fault")}
								allLabel={tu("runs.all")}
								options={(facets?.faults ?? []).map((v) => ({ value: v, label: faultLabel(v) }))}
								value={search.fault}
								onChange={(v) => void setSearch({ fault: v, page: null })}
								className={sel}
								testId="runs-filter-fault"
							/>
						)}
					</div>
					{live > 0 && (
						<span
							className="text-chart-info-ink ml-auto inline-flex items-center gap-1.5 text-xs font-medium"
							data-testid="runs-live-count"
						>
							<span className="bg-chart-info size-1.5 animate-pulse rounded-full" />
							{tu("runs.liveCount", { count: live })}
						</span>
					)}
				</div>
				<ShowArchivedChip
					count={archivedCount}
					on={showArchived}
					onChange={(on) => void setSearch({ archived: on ? "1" : null, page: null })}
					className="self-start"
					testId="runs-show-archived"
				/>
				<FilterTokens tokens={tokens} onClear={clear} testId="runs-tokens" />
			</div>

			{traceQuery && (
				<TraceMatches
					query={traceQuery}
					agent={search.agent}
					outcome={search.outcome}
					suite={search.suite}
					onClear={() => setTraceQuery("")}
				/>
			)}

			{list.isLoading ? (
				<BlockSkeleton rows={8} testId="runs-loading" />
			) : list.error || !list.data ? (
				<LoadError title={tu("runs.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="runs-error" />
			) : rows.length === 0 && pastLastPage(list.data.total, page, RUNS_PAGE) !== null ? (
				<div className="flex flex-col gap-3" data-testid="runs-past-end">
					<p className="text-muted-foreground text-sm">{tw("runs.pastEnd")}</p>
					<div>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								const last = pastLastPage(list.data?.total ?? 0, page, RUNS_PAGE) ?? 1;
								void setSearch({ page: last === 1 ? null : last });
							}}
							dataTestId="runs-last-page"
						>
							{tw("runs.lastPage")}
						</Button>
					</div>
					{pager}
				</div>
			) : rows.length === 0 ? (
				<EmptyState
					icon={ListX}
					kind={active ? "filtered" : "first"}
					body={active ? tu("runs.noMatch") : tu("runs.empty")}
					action={active ? { label: tu("runs.clear"), onClick: clear } : { label: tu("runs.newExperiment"), to: "/evaluate" }}
					testId="runs-empty"
				/>
			) : (
				<>
					{/* The pager above the list too, so the next page is reachable without scrolling 50 rows (X-38). */}
					<div data-testid="runs-pager-top">{pager}</div>
					{/* Phones: stacked rows, task and result on line 1, agent, cost and when on line 2 (M5-28). */}
					<ul className="bg-card divide-y rounded-xl border md:hidden" data-testid="runs-list-mobile">
						{rows.map((row) => (
							<li
								key={row.run_id}
								className={cn("hover:bg-muted/50 flex cursor-pointer flex-col gap-1 px-4 py-2", !row.sealed && "bg-chart-info/5", isArchived(row as ArchivedRun) && ARCHIVED_ROW_CLASS)}
								onClick={(e) => onRowClick(e, row)}
								data-testid={`runs-mrow-${row.run_id}`}
							>
								<div className="flex min-w-0 items-center justify-between gap-3">
									{runLink(row, "min-w-0 truncate text-sm font-medium")}
									<OutcomePill row={row} />
								</div>
								<div className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-x-1.5 text-xs">
									<span className="truncate" title={agentTitle(row)}>
										{runAgent(row)}
										{agentVersionLabel(row) && ` ${agentVersionLabel(row)}`}
									</span>
									<span aria-hidden="true">·</span>
									<span className="tabular-nums">{costCell(row)}</span>
									<span aria-hidden="true">·</span>
									<span className="whitespace-nowrap">{timeAgo(row.started, locale)}</span>
									{isArchived(row as ArchivedRun) && <ArchivedPill at={(row as ArchivedRun).archived_at} testId={`runs-marchived-${row.run_id}`} />}
									<span className="ml-auto">
										<RunRowMenu row={row} testId={`runs-mrow-${row.run_id}`} />
									</span>
								</div>
							</li>
						))}
					</ul>
					<div className="bg-card hidden overflow-x-auto rounded-xl border md:block">
						<table className="w-full text-sm" data-testid="runs-table">
							<thead className="bg-card sticky top-0 z-10">
								<tr className="text-muted-foreground border-b text-xs">
									<SortHeader label={tu("runs.col.task")} sortKey="task" current={search} onSort={onSort} className="px-4 text-left" />
									<SortHeader
										label={tu("runs.col.outcome")}
										sortKey="outcome"
										current={search}
										onSort={onSort}
										className="px-3 text-left"
									/>
									<SortHeader label={tu("runs.col.agent")} sortKey="agent" current={search} onSort={onSort} className="px-3 text-left" />
									<SortHeader
										label={tu("runs.col.steps")}
										sortKey="steps"
										current={search}
										onSort={onSort}
										className="hidden px-3 text-right xl:table-cell"
									/>
									<th scope="col" className="hidden px-3 py-2.5 text-right font-medium 2xl:table-cell">
										{tu("runs.col.toolCalls")}
									</th>
									<SortHeader
										label={tu("runs.col.tokens")}
										sortKey="tokens"
										current={search}
										onSort={onSort}
										className="hidden px-3 text-right xl:table-cell"
									/>
									<SortHeader label={tu("runs.col.cost")} sortKey="cost" current={search} onSort={onSort} className="px-3 text-right" />
									<th scope="col" className="hidden px-3 py-2.5 text-left font-medium 2xl:table-cell">
										{tu("runs.col.fault")}
									</th>
									<SortHeader
										label={tu("runs.col.duration")}
										sortKey="duration"
										current={search}
										onSort={onSort}
										className="hidden px-3 text-right lg:table-cell"
									/>
									<SortHeader label={tu("runs.col.when")} sortKey="started" current={search} onSort={onSort} className="px-4 text-right" />
									<th scope="col" className="w-10 min-w-10 px-2">
										<span className="sr-only">{ta("archive.actions")}</span>
									</th>
								</tr>
							</thead>
							<tbody>
								{rows.map((row) => (
									<tr
										key={row.run_id}
										onClick={(e) => onRowClick(e, row)}
										className={cn(
											"hover:bg-muted/50 cursor-pointer border-b transition-colors last:border-0",
											!row.sealed && "bg-chart-info/5",
											isArchived(row as ArchivedRun) && ARCHIVED_ROW_CLASS,
										)}
										data-testid={`runs-row-${row.run_id}`}
									>
										<td className="px-4 py-2">
											<div className="max-w-44 truncate font-medium lg:max-w-56 xl:max-w-64">{runLink(row)}</div>
											{isArchived(row as ArchivedRun) && <ArchivedPill at={(row as ArchivedRun).archived_at} testId={`runs-archived-${row.run_id}`} />}
											<div className="text-muted-foreground max-w-44 truncate text-xs lg:max-w-56 xl:max-w-64" title={row.suite}>
												{runSuiteLabel(row)}
												{(row as RunListRowNamed).exp_name && (
													<span data-testid={`runs-exp-${row.run_id}`}> · {(row as RunListRowNamed).exp_name}</span>
												)}
											</div>
										</td>
										<td className="px-3 py-2">
											<OutcomePill row={row} />
										</td>
										<td className="px-3 py-2">
											{/* The cell's width comes from this block: a table cell ignores max-width (V5-2). */}
											<span className="block max-w-36 truncate xl:max-w-56" title={agentTitle(row)} data-testid={`runs-agent-${row.run_id}`}>
												<span className="font-medium">{runAgent(row)}</span>
												{agentVersionLabel(row) && <span className="text-muted-foreground"> {agentVersionLabel(row)}</span>}
												{row.model && <span className="text-muted-foreground hidden xl:inline"> · {row.model}</span>}
											</span>
										</td>
										<td className="hidden px-3 py-2 text-right tabular-nums xl:table-cell">{formatNumber(row.steps, locale)}</td>
										<td className="hidden px-3 py-2 text-right tabular-nums 2xl:table-cell">{formatNumber(row.tool_calls, locale)}</td>
										<td className="hidden px-3 py-2 text-right tabular-nums xl:table-cell">{tokensCell(row)}</td>
										<td className="px-3 py-2 text-right whitespace-nowrap">{costCell(row)}</td>
										<td
											className="text-muted-foreground hidden px-3 py-2 text-xs whitespace-nowrap 2xl:table-cell"
											data-testid={`runs-fault-${row.run_id}`}
										>
											{row.fault ? faultLabel(row.fault) : "-"}
										</td>
										<td className="hidden px-3 py-2 text-right tabular-nums lg:table-cell">
											{row.sealed ? formatDuration(runDuration(row)) : "-"}
										</td>
										<td
											className="text-muted-foreground px-4 py-2 text-right text-xs whitespace-nowrap"
											title={row.started ? new Date(row.started).toLocaleString(locale) : undefined}
										>
											{timeAgo(row.started, locale)}
										</td>
										<td className="w-10 min-w-10 px-2 py-2 text-right">
											<RunRowMenu row={row} testId={`runs-row-${row.run_id}`} />
										</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
					{pager}
				</>
			)}
		</div>
	);
}

/** A column header that sorts the list: a button inside the th, with aria-sort on the th. */
function SortHeader({
	label,
	sortKey,
	current,
	onSort,
	className,
}: {
	label: string;
	sortKey: RunSortKey;
	current: { sort?: string | null; dir?: string | null };
	onSort: (key: RunSortKey) => void;
	className: string;
}) {
	const { t } = useTranslation("fw21");
	const state = ariaSortOf(current, sortKey);
	const Icon = state === "ascending" ? ArrowUp : state === "descending" ? ArrowDown : ArrowUpDown;
	const right = className.includes("text-right");
	return (
		<th scope="col" aria-sort={state} className={cn("p-0 font-medium", className)}>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				aria-label={t("runs.sortBy", { column: label })}
				className={cn(
					// The button fills the header cell, at least 36 px tall (M5-30).
					"hover:text-foreground focus-visible:ring-ring inline-flex min-h-9 w-full items-center gap-1 rounded-sm py-2 focus-visible:ring-2 focus-visible:outline-none",
					right && "flex-row-reverse justify-start",
					state !== "none" && "text-foreground",
				)}
				data-testid={`runs-sort-${sortKey}`}
			>
				{label}
				<Icon aria-hidden="true" className={cn("size-3", state === "none" && "opacity-40")} />
			</button>
		</th>
	);
}
