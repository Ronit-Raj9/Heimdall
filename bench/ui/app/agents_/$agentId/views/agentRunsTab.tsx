import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { shortReason } from "@/components/runs/runFailure.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { formatShortTime } from "@/components/runs/runList";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { usd } from "@/components/reportcard/reportCard.utils";
import { getLocale } from "@/lib/i18n";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import type { RunListParams, RunListRow } from "@/lib/types/bench";
import "@/app/agents/views/w4m1I18n";
import "@/lib/types/agentFirstI18n";
import "./uf2cI18n";
import { Link } from "@tanstack/react-router";
import { Gauge } from "lucide-react";
import { parseAsBoolean, parseAsInteger, parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";

/** Runs shown on one page of the agent's Runs tab. */
export const AGENT_RUNS_PAGE = 50;

/** The rows the tab shows: all, or only those that did not pass. */
export function visibleAgentRuns(rows: readonly RunListRow[], failedOnly: boolean): RunListRow[] {
	return failedOnly ? rows.filter((r) => r.outcome !== "pass") : [...rows];
}

/**
 * The list query of one page of the tab. With failedOnly the server leaves out passed runs, so the
 * total and the pager count failed runs only (p01-3). failed is read by GET /runs.
 */
export function agentRunsParams(
	agent: string,
	page: number,
	failedOnly: boolean,
	filters: { suite?: string; outcome?: string } = {},
): RunListParams & { failed?: boolean } {
	const params: RunListParams = { agent, limit: AGENT_RUNS_PAGE, offset: page * AGENT_RUNS_PAGE };
	// The benchmark and result filters (M2-47) are sent only when picked.
	if (filters.suite) params.suite = filters.suite;
	if (filters.outcome) params.outcome = filters.outcome;
	return failedOnly ? { ...params, failed: true } : params;
}

/**
 * Why a run did not pass (M2-42): the server's reason when it sends one, else the first part
 * failure's error, else a plain line for the outcome (a graded failure has no error). Empty for a
 * pass. key is a uf2c locale key the caller translates; text is shown as is.
 */
export function failReasonOf(row: Pick<RunListRow, "outcome" | "error" | "fail_reason">): { text?: string; key?: string } {
	if (row.outcome === "pass") return {};
	const own = shortReason(row.fail_reason) || shortReason(row.error);
	if (own) return { text: own };
	return { key: `runs.reason.${REASON_KEYS.has(row.outcome) ? row.outcome : "other"}` };
}

const REASON_KEYS: ReadonlySet<string> = new Set([
	"fail",
	"timeout",
	"budget",
	"crash",
	"refusal",
	"integrity_violation",
	"invalid_evaluator",
	"infra_error",
	"aborted_infra",
]);

/** The outcomes the result filter offers, in this order. */
export const RESULT_FILTERS = ["pass", "fail", "timeout", "budget", "crash", "integrity_violation", "infra_error"] as const;



/** A run's cost in dollars when the server measured it, else null. */
export function runCost(row: Pick<RunListRow, "cost_usd">): number | null {
	const v = row.cost_usd;
	return v && v.state === "observed" && typeof v.v === "number" ? v.v : null;
}

/** The Runs tab: this agent's newest runs on every benchmark, with outcome, cost and why it failed. */
export default function AgentRunsTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentFirst");
	const { t: tu } = useTranslation("uf2c");
	const locale = getLocale();
	// Page, filters and the failed box live in the URL, so a shared link opens the same list (M2-45).
	const [pageParam, setPageParam] = useQueryState("page", parseAsInteger.withDefault(1).withOptions({ history: "replace" }));
	const [failedOnly, setFailedParam] = useQueryState("failed", parseAsBoolean.withDefault(false).withOptions({ history: "replace" }));
	const [suite, setSuite] = useQueryState("benchmark", parseAsString.withDefault("").withOptions({ history: "replace" }));
	const [outcome, setOutcome] = useQueryState("result", parseAsString.withDefault("").withOptions({ history: "replace" }));
	const page = Math.max(0, pageParam - 1);
	const setPage = (p: number) => void setPageParam(p <= 0 ? null : p + 1);
	const { t: tm } = useTranslation("w4m1");
	const query = useListRunsQuery(agentRunsParams(agentId, page, failedOnly, { suite, outcome }));
	// The server already leaves out passed runs; the filter stays for a server that ignores failed.
	const rows = visibleAgentRuns(query.data?.runs ?? [], failedOnly);
	const total = query.data?.total ?? 0;
	const pages = Math.max(1, Math.ceil(total / AGENT_RUNS_PAGE));
	const suites = query.data?.facets?.suites ?? [];
	const filtered = failedOnly || suite !== "" || outcome !== "";

	if (query.isLoading) return <BlockSkeleton rows={6} testId="agent-runs-loading" />;
	if (query.isError)
		return <LoadError title={t("runs.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agent-runs-error" />;
	const reason = (r: RunListRow) => {
		const why = failReasonOf(r);
		const base = why.text ?? (why.key ? tu(why.key as "runs.reason.other") : "");
		return base && r.fault ? tu("runs.withFault", { reason: base, fault: r.fault }) : base;
	};
	const selectCls = "border-input bg-card h-9 rounded-md border px-2 text-sm";
	const filters = (
		<div className="flex flex-wrap items-center gap-3 sm:ml-auto">
			<label className="inline-flex items-center gap-2 text-sm">
				<span className="text-muted-foreground">{tu("runs.benchmark")}</span>
				<select
					className={selectCls}
					value={suite}
					onChange={(e) => {
						void setSuite(e.target.value || null);
						setPage(0);
					}}
					data-testid="agent-runs-benchmark"
				>
					<option value="">{tu("runs.allBenchmarks")}</option>
					{[...new Set([...suites, ...(suite ? [suite] : [])])].sort().map((s) => (
						<option key={s} value={s}>
							{s}
						</option>
					))}
				</select>
			</label>
			<label className="inline-flex items-center gap-2 text-sm">
				<span className="text-muted-foreground">{tu("runs.result")}</span>
				<select
					className={selectCls}
					value={outcome}
					onChange={(e) => {
						void setOutcome(e.target.value || null);
						setPage(0);
					}}
					data-testid="agent-runs-result"
				>
					<option value="">{tu("runs.allResults")}</option>
					{RESULT_FILTERS.map((o) => (
						<option key={o} value={o}>
							{tu(`runs.resultOption.${o}`)}
						</option>
					))}
				</select>
			</label>
			<label className="inline-flex min-h-9 items-center gap-2 text-sm">
				<input
					type="checkbox"
					checked={failedOnly}
					onChange={(e) => {
						void setFailedParam(e.target.checked ? true : null);
						setPage(0);
					}}
					data-testid="agent-runs-failed-only"
				/>
				{t("runs.failedOnly")}
			</label>
		</div>
	);
	if (total === 0 && filtered) {
		return (
			<div className="flex min-w-0 flex-col gap-3" data-testid="agent-runs-tab">
				<div className="flex flex-wrap items-center gap-3">{filters}</div>
				<EmptyNote testId="agent-runs-none-failed">{failedOnly ? tm("runs.noneFailed") : tu("runs.noneMatch")}</EmptyNote>
			</div>
		);
	}
	if (total === 0) {
		return (
			<EmptyNote testId="agent-runs-empty">
				<span className="flex flex-col items-center gap-3">
					<span>{t("runs.empty")}</span>
					<Button asChild size="sm">
						<Link to={"/evaluate" as never} search={{ agent: agentId } as never} data-testid="agent-runs-evaluate">
							<Gauge aria-hidden="true" />
							{t("runs.evaluate")}
						</Link>
					</Button>
				</span>
			</EmptyNote>
		);
	}
	// The "Why it failed" column hides when no row on the page has a reason to show.
	const showWhy = rows.some((r) => reason(r) !== "");
	const when = (r: RunListRow) => formatShortTime(r.started ?? r.sealed_at, locale) || r.run_id;
	const cost = (r: RunListRow) => (runCost(r) === null ? "-" : usd(runCost(r)));
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="agent-runs-tab">
			<div className="flex flex-wrap items-center gap-3">
				<h2 className="text-sm font-semibold">{t("runs.summary", { count: total })}</h2>
				{filters}
			</div>
			{rows.length === 0 ? (
				<p className="text-muted-foreground rounded-lg border py-6 text-center text-sm" data-testid="agent-runs-none-failed">
					{t("runs.noneFailed")}
				</p>
			) : (
				<>
					{/* Phones: one list row per run, the result first, the whole row opens the run (M2-43). */}
					<ul className="divide-y rounded-lg border sm:hidden" data-testid="agent-runs-list">
						{rows.map((r) => (
							<li key={r.run_id}>
								<Link
									to="/runs/$runId"
									params={{ runId: r.run_id }}
									className="hover:bg-muted/40 flex min-h-11 flex-col gap-1 px-3 py-2 text-sm"
									data-testid={`agent-run-item-${r.run_id}`}
								>
									<span className="flex items-center gap-2">
										<OutcomeBadge outcome={r.outcome} />
										<span className="min-w-0 truncate" title={r.task}>
											{r.task}
										</span>
									</span>
									<span className="text-muted-foreground flex flex-wrap gap-x-3 text-xs tabular-nums">
										<span>{when(r)}</span>
										<span>{r.suite}</span>
										<span>{cost(r)}</span>
									</span>
									{reason(r) && <span className="text-muted-foreground line-clamp-2 text-xs">{reason(r)}</span>}
								</Link>
							</li>
						))}
					</ul>
					<div className="hidden min-w-0 overflow-x-auto rounded-lg border sm:block">
						<Table data-testid="agent-runs-table" className="text-sm">
							<TableHeader>
								<TableRow>
									<TableHead>{t("runs.col.when")}</TableHead>
									<TableHead>{t("runs.col.benchmark")}</TableHead>
									<TableHead>{t("runs.col.task")}</TableHead>
									<TableHead>{t("runs.col.outcome")}</TableHead>
									<TableHead className="text-right">{t("runs.col.cost")}</TableHead>
									{showWhy && <TableHead>{t("runs.col.why")}</TableHead>}
								</TableRow>
							</TableHeader>
							<TableBody>
								{rows.map((r) => (
									// The date link covers the whole row, so any part of it opens the run (M2-44).
									<TableRow key={r.run_id} className="hover:bg-muted/40 relative h-11" data-testid={`agent-run-${r.run_id}`}>
										<TableCell className="whitespace-nowrap">
											<Link
												to="/runs/$runId"
												params={{ runId: r.run_id }}
												className="text-primary underline-offset-4 after:absolute after:inset-0 hover:underline"
											>
												{when(r)}
											</Link>
										</TableCell>
										<TableCell>{r.suite}</TableCell>
										<TableCell className="max-w-[14rem] truncate" title={r.task}>
											{r.task}
										</TableCell>
										<TableCell>
											<OutcomeBadge outcome={r.outcome} />
										</TableCell>
										<TableCell className="text-right tabular-nums">{cost(r)}</TableCell>
										{showWhy && (
											<TableCell className="text-muted-foreground max-w-[22rem] truncate" title={r.error ?? reason(r)}>
												{reason(r)}
											</TableCell>
										)}
									</TableRow>
								))}
							</TableBody>
						</Table>
					</div>
				</>
			)}
			{pages > 1 && (
				<div className="flex items-center justify-end gap-2 text-sm">
					<Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage(page - 1)} data-testid="agent-runs-prev">
						{t("runs.prev")}
					</Button>
					<span className="text-muted-foreground tabular-nums">{t("runs.page", { page: page + 1, pages })}</span>
					<Button
						size="sm"
						variant="outline"
						disabled={page + 1 >= pages}
						onClick={() => setPage(page + 1)}
						data-testid="agent-runs-next"
					>
						{t("runs.next")}
					</Button>
				</div>
			)}
		</div>
	);
}
