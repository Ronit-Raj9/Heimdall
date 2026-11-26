import { faultName } from "@/components/reportcard/reportCard.utils";
import { EmptyState } from "@/components/shell/emptyState";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { outcomeTone } from "@/components/runs/runs.utils";
import { Button } from "@/components/ui/button";
import { FilterSelect, FilterTokens, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import { useGetAgentsQuery, useListRunsQuery } from "@/lib/store/apis/benchApi";
import type { RunListParams, RunListRow } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useNavigate } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, ListX } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useCallback, useMemo, type KeyboardEvent } from "react";
import { TraceMatches, TraceSearchInput } from "./traceMatches";
import { pastLastPage } from "@/components/runs/runList";
import "./w4m4I18n";

export { pastLastPage };
import { useTranslation } from "react-i18next";
import "./fw21I18n";

export const RUNS_PAGE = 50;

const parsers = {
	outcome: parseAsString,
	suite: parseAsString,
	arm: parseAsString,
	agent: parseAsString,
	fault: parseAsString,
	q: parseAsString,
	tq: parseAsString,
	page: parseAsInteger,
	sort: parseAsString,
	dir: parseAsString,
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

const PILL = {
	pass: "bg-chart-success/12 text-chart-success-ink",
	zero: "bg-chart-error/12 text-chart-error-ink",
	unknown: "bg-muted text-muted-foreground",
	live: "bg-chart-info/12 text-chart-info-ink",
} as const;

const DOT = {
	pass: "bg-chart-success",
	zero: "bg-chart-error",
	unknown: "bg-muted-foreground/60",
	live: "bg-chart-info",
} as const;

/** Outcome as a soft pill with a coloured dot; a running run pulses. */
export function OutcomePill({ row }: { row: Pick<RunListRow, "sealed" | "outcome" | "run_id"> }) {
	const { t: tu } = useTranslation("u12");
	const { t: tb } = useTranslation("bench");
	const tone = row.sealed ? outcomeTone(row.outcome) : "live";
	return (
		<span
			className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap", PILL[tone])}
			data-testid={`runs-outcome-${row.run_id}`}
			data-outcome={row.sealed ? row.outcome : "running"}
		>
			<span className="relative flex size-1.5">
				{tone === "live" && <span className={cn("absolute inline-flex size-full animate-ping rounded-full opacity-75", DOT.live)} />}
				<span className={cn("relative inline-flex size-1.5 rounded-full", DOT[tone])} />
			</span>
			{row.sealed ? tb(`runs.outcome.${row.outcome}`, { defaultValue: row.outcome }) : tu("runs.live")}
		</span>
	);
}

/** /runs: live runs on top, toolbar filters in the URL, a search box, and one click to open a run. */
export function RunsTable() {
	const { t: tu } = useTranslation("u12");
	const { t: tb } = useTranslation("bench");
	const { t: tw } = useTranslation("w4m4");
	const locale = getLocale();
	const navigate = useNavigate();
	const [search, setSearch] = useQueryStates(parsers, { history: "replace" });
	const list = useListRunsQuery(runsRequest(search, search.page));
	const agentsQuery = useGetAgentsQuery();
	const sorted = parseRunSort(search.sort, search.dir) !== null;
	const rows = useMemo(() => orderRuns(list.data?.runs ?? [], search.q, search.agent, sorted), [list.data, search.q, search.agent, sorted]);
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
	const onKey = (e: KeyboardEvent, row: RunListRow) => {
		if (e.key === "Enter" || e.key === " ") {
			e.preventDefault();
			open(row);
		}
	};

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
					<TraceSearchInput value={search.tq ?? ""} onChange={setTraceQuery} />
					<FilterSelect
						label={tu("runs.outcome")}
						allLabel={tu("runs.all")}
						options={(facets?.outcomes ?? []).map((o) => ({ value: o, label: outcomeLabel(o) }))}
						value={search.outcome}
						onChange={(v) => void setSearch({ outcome: v, page: null })}
						testId="runs-filter-outcome"
					/>
					<FilterSelect
						label={tu("runs.suite")}
						allLabel={tu("runs.all")}
						options={(facets?.suites ?? []).map((v) => ({ value: v, label: v.replace(/@.*$/, "") }))}
						value={search.suite}
						onChange={(v) => void setSearch({ suite: v, page: null })}
						testId="runs-filter-suite"
					/>
					<FilterSelect
						label={tu("runs.agent")}
						allLabel={tu("runs.all")}
						options={agents.map((v) => ({ value: v, label: v }))}
						value={search.agent}
						onChange={(v) => void setSearch({ agent: v, page: null })}
						testId="runs-filter-agent"
					/>
					{(facets?.faults ?? []).some((f) => f !== "clean") && (
						<FilterSelect
							label={tu("runs.fault")}
							allLabel={tu("runs.all")}
							options={(facets?.faults ?? []).map((v) => ({ value: v, label: faultLabel(v) }))}
							value={search.fault}
							onChange={(v) => void setSearch({ fault: v, page: null })}
							testId="runs-filter-fault"
						/>
					)}
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
					<Pager page={page} total={list.data.total} onPage={(n) => void setSearch({ page: n === 1 ? null : n })} />
				</div>
			) : rows.length === 0 ? (
				<EmptyState
					icon={ListX}
					message={active ? tu("runs.noMatch") : tu("runs.empty")}
					action={active ? { label: tu("runs.clear"), onClick: clear } : { label: tu("runs.newExperiment"), to: "/experiments/new" }}
					testId="runs-empty"
				/>
			) : (
				<>
					<div className="bg-card overflow-x-auto rounded-xl border">
						<table className="w-full min-w-[960px] text-sm" data-testid="runs-table">
							<thead className="bg-card sticky top-0 z-10">
								<tr className="text-muted-foreground border-b text-xs">
									<SortHeader label={tu("runs.col.task")} sortKey="task" current={search} onSort={onSort} className="px-4 text-left" />
									<SortHeader label={tu("runs.col.agent")} sortKey="agent" current={search} onSort={onSort} className="px-3 text-left" />
									<SortHeader
										label={tu("runs.col.outcome")}
										sortKey="outcome"
										current={search}
										onSort={onSort}
										className="px-3 text-left"
									/>
									<SortHeader label={tu("runs.col.steps")} sortKey="steps" current={search} onSort={onSort} className="px-3 text-right" />
									<th scope="col" className="px-3 py-2.5 text-right font-medium">
										{tu("runs.col.toolCalls")}
									</th>
									<SortHeader label={tu("runs.col.tokens")} sortKey="tokens" current={search} onSort={onSort} className="px-3 text-right" />
									<SortHeader label={tu("runs.col.cost")} sortKey="cost" current={search} onSort={onSort} className="px-3 text-right" />
									<th scope="col" className="px-3 py-2.5 text-left font-medium">
										{tu("runs.col.fault")}
									</th>
									<SortHeader
										label={tu("runs.col.duration")}
										sortKey="duration"
										current={search}
										onSort={onSort}
										className="px-3 text-right"
									/>
									<SortHeader label={tu("runs.col.when")} sortKey="started" current={search} onSort={onSort} className="px-4 text-right" />
								</tr>
							</thead>
							<tbody>
								{rows.map((row) => (
									<tr
										key={row.run_id}
										tabIndex={0}
										role="link"
										aria-label={tu("runs.open", { run: row.run_id })}
										onClick={() => open(row)}
										onKeyDown={(e) => onKey(e, row)}
										className={cn(
											"hover:bg-muted/50 focus-visible:bg-muted/50 cursor-pointer border-b transition-colors last:border-0 focus-visible:outline-none",
											!row.sealed && "bg-chart-info/5",
										)}
										data-testid={`runs-row-${row.run_id}`}
									>
										<td className="max-w-64 px-4 py-3">
											<div className="truncate font-medium" title={row.task}>
												{row.task}
											</div>
											<div className="text-muted-foreground truncate font-mono text-[11px]" title={row.suite}>
												{row.suite}
											</div>
										</td>
										<td className="max-w-56 px-3 py-3">
											<span className="truncate" title={agentTitle(row)} data-testid={`runs-agent-${row.run_id}`}>
												<span className="font-medium">{runAgent(row)}</span>
												{agentVersionLabel(row) && <span className="text-muted-foreground"> {agentVersionLabel(row)}</span>}
												{row.model && <span className="text-muted-foreground"> · {row.model}</span>}
											</span>
										</td>
										<td className="px-3 py-3">
											<OutcomePill row={row} />
										</td>
										<td className="px-3 py-3 text-right tabular-nums">{formatNumber(row.steps, locale)}</td>
										<td className="px-3 py-3 text-right tabular-nums">{formatNumber(row.tool_calls, locale)}</td>
										<td className="px-3 py-3 text-right tabular-nums">{formatNumber(row.tokens, locale)}</td>
										<td className="px-3 py-3 text-right">
											<ValueCell value={row.cost_usd} kind="usd" />
										</td>
										<td className="text-muted-foreground px-3 py-3 text-xs whitespace-nowrap" data-testid={`runs-fault-${row.run_id}`}>
											{row.fault ? faultLabel(row.fault) : "-"}
										</td>
										<td className="px-3 py-3 text-right tabular-nums">{row.sealed ? formatDuration(runDuration(row)) : "-"}</td>
										<td
											className="text-muted-foreground px-4 py-3 text-right text-xs whitespace-nowrap"
											title={row.started ? new Date(row.started).toLocaleString(locale) : undefined}
										>
											{timeAgo(row.started, locale)}
										</td>
									</tr>
								))}
							</tbody>
						</table>
					</div>
					<Pager page={page} total={list.data.total} onPage={(n) => void setSearch({ page: n === 1 ? null : n })} />
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
		<th scope="col" aria-sort={state} className={cn("py-2.5 font-medium", className)}>
			<button
				type="button"
				onClick={() => onSort(sortKey)}
				aria-label={t("runs.sortBy", { column: label })}
				className={cn(
					"hover:text-foreground focus-visible:ring-ring inline-flex items-center gap-1 rounded-sm focus-visible:ring-2 focus-visible:outline-none",
					right && "flex-row-reverse",
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

function Pager({ page, total, onPage }: { page: number; total: number; onPage: (page: number) => void }) {
	const { t } = useTranslation("u12");
	const pages = Math.max(1, Math.ceil(total / RUNS_PAGE));
	const first = (page - 1) * RUNS_PAGE + 1;
	const last = Math.min(total, page * RUNS_PAGE);
	return (
		<nav className="flex items-center justify-between gap-2 text-xs" data-testid="runs-pager">
			<span className="text-muted-foreground tabular-nums">{t("runs.showing", { first, last, total })}</span>
			{pages > 1 && (
				<span className="flex items-center gap-1">
					<Button
						variant="ghost"
						size="sm"
						disabled={page <= 1}
						onClick={() => onPage(page - 1)}
						aria-label={t("runs.prev")}
						dataTestId="runs-prev"
					>
						<ChevronLeft aria-hidden="true" />
					</Button>
					<span className="text-muted-foreground px-1 tabular-nums">
						{page} / {pages}
					</span>
					<Button
						variant="ghost"
						size="sm"
						disabled={page >= pages}
						onClick={() => onPage(page + 1)}
						aria-label={t("runs.next")}
						dataTestId="runs-next"
					>
						<ChevronRight aria-hidden="true" />
					</Button>
				</span>
			)}
		</nav>
	);
}