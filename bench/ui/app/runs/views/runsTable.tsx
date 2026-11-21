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
import { ChevronLeft, ChevronRight, ListX } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryStates } from "nuqs";
import { useMemo, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";

export const RUNS_PAGE = 50;

const parsers = {
	outcome: parseAsString,
	suite: parseAsString,
	arm: parseAsString,
	agent: parseAsString,
	q: parseAsString,
	page: parseAsInteger,
};

/** The server request for the filters and page; the search box filters the page on the client. */
export function runsRequest(f: { outcome?: string | null; suite?: string | null; arm?: string | null }, page: number | null): RunListParams {
	const params: RunListParams = { limit: RUNS_PAGE };
	if (f.outcome) params.outcome = f.outcome;
	if (f.suite) params.suite = f.suite;
	if (f.arm) params.arm = f.arm;
	const p = Math.max(1, page ?? 1);
	if (p > 1) params.offset = (p - 1) * RUNS_PAGE;
	return params;
}

/** The agent a run used: its agent config id, or the arm id when the run has none. */
export function runAgent(r: Pick<RunListRow, "config_id" | "arm">): string {
	return r.config_id || r.arm;
}

/** Agent names for the Agent filter: every configured agent plus any agent seen on the page. Never arm ids. */
export function agentOptions(configured: readonly string[], rows: readonly Pick<RunListRow, "config_id">[]): string[] {
	const seen = new Set(configured.filter(Boolean));
	for (const r of rows) if (r.config_id) seen.add(r.config_id);
	return [...seen].sort((a, b) => a.localeCompare(b));
}

/** Running runs first, then the server's newest-first order; a search narrows by task, agent or model, the agent filter by agent name. */
export function orderRuns(rows: readonly RunListRow[], query: string | null, agent: string | null = null): RunListRow[] {
	const q = (query ?? "").trim().toLowerCase();
	const hit = (r: RunListRow) => !q || [r.task, r.arm, r.config_id ?? "", r.model ?? "", r.suite].some((v) => v.toLowerCase().includes(q));
	const shown = rows.filter((r) => hit(r) && (!agent || runAgent(r) === agent));
	return [...shown.filter((r) => !r.sealed), ...shown.filter((r) => r.sealed)];
}

/** Seconds between start and seal, or null while either is missing. */
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
	const locale = getLocale();
	const navigate = useNavigate();
	const [search, setSearch] = useQueryStates(parsers, { history: "replace" });
	const list = useListRunsQuery(runsRequest(search, search.page));
	const agentsQuery = useGetAgentsQuery();
	const rows = useMemo(() => orderRuns(list.data?.runs ?? [], search.q, search.agent), [list.data, search.q, search.agent]);
	const agents = useMemo(
		() => agentOptions((agentsQuery.data?.agents ?? []).map((a) => a.id), list.data?.runs ?? []),
		[agentsQuery.data, list.data],
	);
	const live = rows.filter((r) => !r.sealed).length;
	const active = Boolean(search.outcome || search.suite || search.arm || search.agent || search.q);
	const page = Math.max(1, search.page ?? 1);
	const facets = list.data?.facets;
	const clear = () => void setSearch({ outcome: null, suite: null, arm: null, agent: null, q: null, page: null });
	const outcomeLabel = (o: string) => tb(`runs.outcome.${o}`, { defaultValue: o });
	const tokens = ([
		search.outcome && { key: "outcome", label: `${tu("runs.outcome")}: ${outcomeLabel(search.outcome)}`, onRemove: () => void setSearch({ outcome: null, page: null }) },
		search.suite && { key: "suite", label: `${tu("runs.suite")}: ${search.suite.replace(/@.*$/, "")}`, onRemove: () => void setSearch({ suite: null, page: null }) },
		search.agent && { key: "agent", label: `${tu("runs.agent")}: ${search.agent}`, onRemove: () => void setSearch({ agent: null, page: null }) },
		// An older link may still carry an arm id; show it so it can be removed.
		search.arm && { key: "arm", label: `${tu("runs.agent")}: ${search.arm}`, onRemove: () => void setSearch({ arm: null, page: null }) },
	] as (FilterToken | false | "" | null | undefined)[]).filter((x): x is FilterToken => Boolean(x));
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
					{live > 0 && (
						<span className="text-chart-info-ink ml-auto inline-flex items-center gap-1.5 text-xs font-medium" data-testid="runs-live-count">
							<span className="bg-chart-info size-1.5 animate-pulse rounded-full" />
							{tu("runs.liveCount", { count: live })}
						</span>
					)}
				</div>
				<FilterTokens tokens={tokens} onClear={clear} testId="runs-tokens" />
			</div>

			{list.isLoading ? (
				<BlockSkeleton rows={8} testId="runs-loading" />
			) : list.error || !list.data ? (
				<LoadError title={tu("runs.loadError")} error={list.error} onRetry={() => void list.refetch()} testId="runs-error" />
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
						<table className="w-full min-w-[760px] text-sm" data-testid="runs-table">
							<thead className="bg-card sticky top-0 z-10">
								<tr className="text-muted-foreground border-b text-xs">
									<th scope="col" className="px-4 py-2.5 text-left font-medium">
										{tu("runs.col.task")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-left font-medium">
										{tu("runs.col.agent")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-left font-medium">
										{tu("runs.col.outcome")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-right font-medium">
										{tu("runs.col.steps")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-right font-medium">
										{tu("runs.col.tokens")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-right font-medium">
										{tu("runs.col.cost")}
									</th>
									<th scope="col" className="px-3 py-2.5 text-right font-medium">
										{tu("runs.col.duration")}
									</th>
									<th scope="col" className="px-4 py-2.5 text-right font-medium">
										{tu("runs.col.when")}
									</th>
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
											<span className="truncate" title={row.model ? `${runAgent(row)} · ${row.model}` : runAgent(row)}>
												<span className="font-medium">{runAgent(row)}</span>
												{row.model && <span className="text-muted-foreground"> · {row.model}</span>}
											</span>
										</td>
										<td className="px-3 py-3">
											<OutcomePill row={row} />
										</td>
										<td className="px-3 py-3 text-right tabular-nums">{formatNumber(row.steps, locale)}</td>
										<td className="px-3 py-3 text-right tabular-nums">{formatNumber(row.tokens, locale)}</td>
										<td className="px-3 py-3 text-right">
											<ValueCell value={row.cost_usd} kind="usd" />
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
					<Button variant="ghost" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label={t("runs.prev")} dataTestId="runs-prev">
						<ChevronLeft aria-hidden="true" />
					</Button>
					<span className="text-muted-foreground px-1 tabular-nums">
						{page} / {pages}
					</span>
					<Button variant="ghost" size="sm" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label={t("runs.next")} dataTestId="runs-next">
						<ChevronRight aria-hidden="true" />
					</Button>
				</span>
			)}
		</nav>
	);
}
