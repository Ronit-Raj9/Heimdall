import { OutcomeBadge } from "@/components/runs/outcomeBadge";
import { shortReason } from "@/components/runs/runFailure.utils";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { formatShortTime } from "@/components/runs/runList";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { usd } from "@/components/reportcard/reportCard.utils";
import { getLocale } from "@/lib/i18n";
import { useListRunsQuery } from "@/lib/store/apis/benchApi";
import type { RunListRow } from "@/lib/types/bench";
import "@/lib/types/agentFirstI18n";
import { Link } from "@tanstack/react-router";
import { Gauge } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** Runs shown on one page of the agent's Runs tab. */
export const AGENT_RUNS_PAGE = 50;

/** The rows the tab shows: all, or only those that did not pass. */
export function visibleAgentRuns(rows: readonly RunListRow[], failedOnly: boolean): RunListRow[] {
	return failedOnly ? rows.filter((r) => r.outcome !== "pass") : [...rows];
}

/** A run's cost in dollars when the server measured it, else null. */
export function runCost(row: Pick<RunListRow, "cost_usd">): number | null {
	const v = row.cost_usd;
	return v && v.state === "observed" && typeof v.v === "number" ? v.v : null;
}

/** The Runs tab: this agent's newest runs on every benchmark, with outcome, cost and why it failed. */
export default function AgentRunsTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentFirst");
	const locale = getLocale();
	const [page, setPage] = useState(0);
	const [failedOnly, setFailedOnly] = useState(false);
	const query = useListRunsQuery({ agent: agentId, limit: AGENT_RUNS_PAGE, offset: page * AGENT_RUNS_PAGE });
	const rows = visibleAgentRuns(query.data?.runs ?? [], failedOnly);
	const total = query.data?.total ?? 0;
	const pages = Math.max(1, Math.ceil(total / AGENT_RUNS_PAGE));

	if (query.isLoading) return <BlockSkeleton rows={6} testId="agent-runs-loading" />;
	if (query.isError) return <LoadError title={t("runs.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="agent-runs-error" />;
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
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="agent-runs-tab">
			<div className="flex flex-wrap items-center gap-3">
				<p className="text-muted-foreground text-sm">{t("runs.summary", { count: total })}</p>
				<label className="ml-auto inline-flex items-center gap-2 text-sm">
					<input type="checkbox" checked={failedOnly} onChange={(e) => setFailedOnly(e.target.checked)} data-testid="agent-runs-failed-only" />
					{t("runs.failedOnly")}
				</label>
			</div>
			<div className="min-w-0 overflow-x-auto rounded-lg border">
				<Table data-testid="agent-runs-table">
					<TableHeader>
						<TableRow>
							<TableHead>{t("runs.col.when")}</TableHead>
							<TableHead>{t("runs.col.benchmark")}</TableHead>
							<TableHead>{t("runs.col.task")}</TableHead>
							<TableHead>{t("runs.col.outcome")}</TableHead>
							<TableHead className="text-right">{t("runs.col.cost")}</TableHead>
							<TableHead>{t("runs.col.why")}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{rows.length === 0 ? (
							<TableRow>
								<TableCell colSpan={6} className="text-muted-foreground py-6 text-center text-sm" data-testid="agent-runs-none-failed">
									{t("runs.noneFailed")}
								</TableCell>
							</TableRow>
						) : (
							rows.map((r) => (
								<TableRow key={r.run_id} data-testid={`agent-run-${r.run_id}`}>
									<TableCell className="text-xs whitespace-nowrap">
										<Link to="/runs/$runId" params={{ runId: r.run_id }} className="text-primary underline-offset-4 hover:underline">
											{formatShortTime(r.started ?? r.sealed_at, locale) || r.run_id}
										</Link>
									</TableCell>
									<TableCell className="font-mono text-xs">{r.suite}</TableCell>
									<TableCell className="max-w-[14rem] truncate font-mono text-xs" title={r.task}>
										{r.task}
									</TableCell>
									<TableCell>
										<OutcomeBadge outcome={r.outcome} />
									</TableCell>
									<TableCell className="text-right text-xs tabular-nums">{runCost(r) === null ? "-" : usd(runCost(r))}</TableCell>
									<TableCell className="text-muted-foreground max-w-[22rem] truncate text-xs" title={r.error}>
										{r.outcome === "pass" ? "" : shortReason(r.error) || "-"}
									</TableCell>
								</TableRow>
							))
						)}
					</TableBody>
				</Table>
			</div>
			{pages > 1 && (
				<div className="flex items-center justify-end gap-2 text-sm">
					<Button size="sm" variant="outline" disabled={page === 0} onClick={() => setPage((p) => p - 1)} data-testid="agent-runs-prev">
						{t("runs.prev")}
					</Button>
					<span className="text-muted-foreground tabular-nums">{t("runs.page", { page: page + 1, pages })}</span>
					<Button size="sm" variant="outline" disabled={page + 1 >= pages} onClick={() => setPage((p) => p + 1)} data-testid="agent-runs-next">
						{t("runs.next")}
					</Button>
				</div>
			)}
		</div>
	);
}
