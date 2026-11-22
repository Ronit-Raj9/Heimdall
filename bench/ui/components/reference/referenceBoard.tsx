import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { suiteDisplayName } from "@/components/suites/suiteDisplay";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getLocale } from "@/lib/i18n";
import { useGetReferenceBoardQuery, type ReferenceBoard, type ReferenceEntry } from "@/lib/store/apis/referenceApi";
import "@/lib/types/referenceI18n";
import { ExternalLink, Fingerprint } from "lucide-react";
import { useTranslation } from "react-i18next";
import { agentName, costPerSolved, groupByBenchmark, percent, shortHash } from "./reference.utils";

function day(iso: string | undefined): string {
	if (!iso) return "";
	const d = new Date(iso);
	if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return "";
	return d.toLocaleDateString(getLocale(), { year: "numeric", month: "short", day: "numeric" });
}

/** One benchmark's reference rows. */
export function ReferenceTable({ entries, testId }: { entries: readonly ReferenceEntry[]; testId: string }) {
	const { t } = useTranslation("reference");
	return (
		<div className="bg-card min-w-0 overflow-x-auto rounded-xl border" data-testid={testId}>
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{t("col.agent")}</TableHead>
						<TableHead>{t("col.model")}</TableHead>
						<TableHead className="text-right">{t("col.score")}</TableHead>
						<TableHead className="text-right">{t("col.range")}</TableHead>
						<TableHead className="text-right">{t("col.costPerSolved")}</TableHead>
						<TableHead className="text-right">{t("col.runs")}</TableHead>
						<TableHead>{t("col.date")}</TableHead>
						<TableHead>{t("col.fingerprint")}</TableHead>
						<TableHead>{t("col.report")}</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{entries.map((e) => (
						<TableRow key={e.id} data-testid={`reference-row-${e.id}`}>
							<TableCell className="font-medium">{agentName(e)}</TableCell>
							<TableCell className="font-mono text-xs">{e.model}</TableCell>
							<TableCell className="text-right font-semibold tabular-nums">{percent(e.score)}</TableCell>
							<TableCell className="text-muted-foreground text-right tabular-nums">
								{t("cell.range", { low: percent(e.ci_low), high: percent(e.ci_high) })}
							</TableCell>
							<TableCell className="text-right tabular-nums">
								{e.cost_per_solved_usd !== null && e.cost_per_solved_usd !== undefined
									? costPerSolved(e.cost_per_solved_usd)
									: e.cost_usd !== null && e.cost_usd !== undefined
										? t("cell.noneSolved")
										: t("cell.costUnknown")}
							</TableCell>
							<TableCell className="text-muted-foreground text-right tabular-nums" title={t("cell.runs", { runs: e.runs, tasks: e.tasks })}>
								{e.runs}
							</TableCell>
							<TableCell className="text-muted-foreground whitespace-nowrap">{day(e.ran_at)}</TableCell>
							<TableCell>
								<span className="text-muted-foreground inline-flex items-center gap-1 font-mono text-xs" title={t("cell.fingerprintHint", { runs: e.sealed_runs }) + `: ${e.seal_hash}`}>
									<Fingerprint className="size-3.5" aria-hidden="true" />
									{shortHash(e.seal_hash)}
								</span>
							</TableCell>
							<TableCell>
								{e.report_path ? (
									<a className="text-primary inline-flex items-center gap-1 text-sm hover:underline" href={e.report_path} data-testid={`reference-report-${e.id}`}>
										{t("cell.open")}
										<ExternalLink className="size-3.5" aria-hidden="true" />
									</a>
								) : (
									<span className="text-muted-foreground text-sm">{t("cell.noReport")}</span>
								)}
							</TableCell>
						</TableRow>
					))}
				</TableBody>
			</Table>
		</div>
	);
}

/** The whole board, one table per benchmark; an empty board says so plainly. */
export function ReferenceBoardView({ board, benchmark }: { board: ReferenceBoard; benchmark?: string }) {
	const { t } = useTranslation("reference");
	if (board.entries.length === 0) {
		return <EmptyNote testId="reference-empty">{benchmark ? t("emptyBenchmark") : t("empty")}</EmptyNote>;
	}
	const groups = groupByBenchmark(board.entries);
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="reference-board">
			{groups.map((g) => (
				<section key={g.benchmark} className="flex min-w-0 flex-col gap-2" data-testid={`reference-benchmark-${g.benchmark}`}>
					{!benchmark && (
						<h3 className="flex items-baseline gap-2 text-sm font-medium">
							{suiteDisplayName(g.benchmark)}
							<span className="text-muted-foreground font-mono text-xs font-normal">{g.benchmark}</span>
						</h3>
					)}
					<ReferenceTable entries={g.entries} testId={`reference-table-${g.benchmark}`} />
				</section>
			))}
			<p className="text-muted-foreground text-xs text-pretty" data-testid="reference-provenance">
				{t("provenance", { org: board.org })}
				{board.updated_at && day(board.updated_at) ? ` ${t("updated", { date: day(board.updated_at) })}.` : ""}
			</p>
		</div>
	);
}

/** The board loaded from the server, optionally one benchmark only. */
export function ReferenceBoardPanel({ benchmark }: { benchmark?: string }) {
	const { t } = useTranslation("reference");
	const q = useGetReferenceBoardQuery(benchmark ? { benchmark } : undefined);
	if (q.isLoading) return <BlockSkeleton rows={4} testId="reference-loading" />;
	if (q.isError || !q.data) return <LoadError title={t("loadError")} error={q.error} onRetry={() => void q.refetch()} testId="reference-error" />;
	return <ReferenceBoardView board={q.data} benchmark={benchmark} />;
}
