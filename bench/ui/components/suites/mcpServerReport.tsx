import { BlockSkeleton, EmptyNote, LoadError, Section } from "@/components/runs/screenStates";
import { isNotFoundError } from "@/lib/store/apis/notFound";
import { useGetMcpServerReportQuery } from "@/lib/store/apis/ufl5Api";
import type { McpServerReport, McpToolStats } from "@/lib/types/ufl5";
import { useTranslation } from "react-i18next";
import "./ufl5I18n";

/** A share as a whole percent; "-" when nothing was counted. */
export function pct(rate: number, of: number): string {
	return of > 0 ? `${Math.round(rate * 100)}%` : "-";
}

/** The tools of a report, most called first; ties by name. */
export function sortedTools(rep: McpServerReport): McpToolStats[] {
	return [...(rep.tools ?? [])].sort((a, b) => b.calls - a.calls || a.tool.localeCompare(b.tool));
}

/**
 * Server report of a benchmark drafted from an MCP server (GET /mcp-servers/benchmarks/{id}/report):
 * per tool, how often agents picked it, sent valid arguments, got a result, and passed.
 * hideUnlessDrafted: on a benchmark page, a benchmark not drafted from an MCP server (404) shows nothing.
 */
export function McpServerReportSection({ benchmark, hideUnlessDrafted = false }: { benchmark: string; hideUnlessDrafted?: boolean }) {
	const { t } = useTranslation("ufl5");
	const q = useGetMcpServerReportQuery(benchmark);
	if (hideUnlessDrafted && (q.isLoading || (q.error && isNotFoundError(q.error)))) return null;
	return (
		<Section title={t("mcpReport.title")} as="h3" testId="mcp-server-report">
			{q.isLoading ? (
				<BlockSkeleton rows={3} testId="mcp-server-report-loading" />
			) : q.error ? (
				<LoadError title={t("mcpReport.loadError")} error={q.error} onRetry={() => void q.refetch()} testId="mcp-server-report-error" />
			) : q.data ? (
				<McpServerReportBody rep={q.data} />
			) : null}
		</Section>
	);
}

export function McpServerReportBody({ rep }: { rep: McpServerReport }) {
	const { t } = useTranslation("ufl5");
	if (rep.runs === 0) return <EmptyNote testId="mcp-server-report-empty">{t("mcpReport.empty")}</EmptyNote>;
	const tools = sortedTools(rep);
	const advice = tools.flatMap((x) => (x.advice ?? []).map((a) => ({ tool: x.tool, a })));
	return (
		<div className="flex min-w-0 flex-col gap-3 text-sm">
			<p className="text-muted-foreground" data-testid="mcp-server-report-summary">
				{t("mcpReport.summary", { runs: rep.runs, agents: (rep.agents ?? []).join(", ") || "-", calls: rep.calls, errors: rep.errors })}
			</p>
			<div className="bg-card min-w-0 overflow-x-auto rounded-xl border">
				<table className="w-full text-sm">
					<thead className="text-muted-foreground border-b text-left text-xs">
						<tr>
							<th className="px-4 py-2 font-medium">{t("mcpReport.tool")}</th>
							<th className="px-4 py-2 text-right font-medium">{t("mcpReport.picked")}</th>
							<th className="px-4 py-2 text-right font-medium">{t("mcpReport.valid")}</th>
							<th className="px-4 py-2 text-right font-medium">{t("mcpReport.succeeded")}</th>
							<th className="hidden px-4 py-2 text-right font-medium sm:table-cell">{t("mcpReport.passed")}</th>
							<th className="hidden px-4 py-2 text-right font-medium sm:table-cell">{t("mcpReport.latency")}</th>
						</tr>
					</thead>
					<tbody>
						{tools.map((x) => (
							<tr key={x.tool} className="border-b last:border-0" data-testid={`mcp-server-report-tool-${x.tool}`}>
								<td className="px-4 py-2 font-mono text-xs">{x.tool}</td>
								<td className="px-4 py-2 text-right tabular-nums" title={`${x.picked}/${x.runs}`}>
									{pct(x.pick_rate, x.runs)}
								</td>
								<td className="px-4 py-2 text-right tabular-nums" title={`${x.valid}/${x.calls}`}>
									{pct(x.valid_rate, x.calls)}
								</td>
								<td className="px-4 py-2 text-right tabular-nums" title={`${x.succeeded}/${x.calls}`}>
									{pct(x.success_rate, x.calls)}
								</td>
								<td className="hidden px-4 py-2 text-right tabular-nums sm:table-cell">{x.runs > 0 ? `${x.passed}/${x.runs}` : "-"}</td>
								<td className="text-muted-foreground hidden px-4 py-2 text-right text-xs tabular-nums sm:table-cell">
									{x.calls > 0 ? t("mcpReport.ms", { p50: x.p50_ms, p95: x.p95_ms }) : "-"}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</div>
			{advice.length > 0 && (
				<details data-testid="mcp-server-report-advice">
					<summary className="text-primary min-h-11 cursor-pointer py-2 font-medium">{t("mcpReport.advice", { n: advice.length })}</summary>
					<ul className="flex list-disc flex-col gap-1 pl-5">
						{advice.map((x, i) => (
							<li key={i}>
								<span className="font-mono text-xs">{x.tool}</span>: {x.a}
							</li>
						))}
					</ul>
				</details>
			)}
			{(rep.unlisted_tools ?? []).length > 0 && (
				<p className="text-chart-warning-ink" data-testid="mcp-server-report-unlisted">
					{t("mcpReport.unlisted", { tools: (rep.unlisted_tools ?? []).join(", ") })}
				</p>
			)}
		</div>
	);
}