import { ReportCard } from "@/components/reportcard/reportCard";
import { ReportFailures } from "@/components/reportcard/reportFailures";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetAgentReportCardQuery, useGetAgentVersionsQuery, type ReportCardResponse } from "@/lib/store/apis/evaluationsApi";
import { useGetModelsQuery } from "@/lib/store/apis/modelsApi";
import { Link } from "@tanstack/react-router";
import { Gauge } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";

/** True when the server had no completed evaluation for this agent, version and model. */
export function isEmptyReport(report: ReportCardResponse | undefined, error: unknown): boolean {
	if (error && typeof error === "object" && "status" in error && (error as { status: unknown }).status === 404) return true;
	if (!report) return false;
	return !report.evaluation || ((report.capabilities ?? []).length === 0 && (report.benchmarks ?? []).length === 0);
}

/** Model ids the catalog lists, once each, sorted. */
export function catalogModels(providers: Array<{ models: Array<{ id: string }> }> | undefined): string[] {
	return [...new Set((providers ?? []).flatMap((p) => p.models.map((m) => m.id)))].sort();
}

/** The Report card tab: pick a version and a model, see the latest report card for that pair. */
export default function ReportCardTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const [version, setVersion] = useState(0);
	const [model, setModel] = useState("");
	const versions = useGetAgentVersionsQuery(agentId);
	const models = useGetModelsQuery();
	const report = useGetAgentReportCardQuery({ agent: agentId, version: version || undefined, model: model || undefined });
	const versionList = [...(versions.data?.versions ?? [])].sort((x, y) => y.version - x.version);
	const modelList = catalogModels(models.data?.providers);
	const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
	const empty = isEmptyReport(report.data, report.error);

	return (
		<div className="flex min-w-0 flex-col gap-5" data-testid="agent-report-card-tab">
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-1 text-xs font-medium">
					{t("reportCard.version")}
					<select className={selectClass} value={version} onChange={(e) => setVersion(Number(e.target.value))} data-testid="agent-report-card-version">
						<option value={0}>{t("reportCard.latest")}</option>
						{versionList.map((v) => (
							<option key={v.version} value={v.version}>
								v{v.version}
								{v.note ? ` - ${v.note}` : ""}
							</option>
						))}
					</select>
				</label>
				<label className="flex flex-col gap-1 text-xs font-medium">
					{t("reportCard.model")}
					<select className={selectClass} value={model} onChange={(e) => setModel(e.target.value)} data-testid="agent-report-card-model">
						<option value="">{t("reportCard.anyModel")}</option>
						{modelList.map((id) => (
							<option key={id} value={id}>
								{id}
							</option>
						))}
					</select>
				</label>
			</div>
			{report.isLoading ? (
				<BlockSkeleton rows={6} testId="agent-report-card-loading" />
			) : empty ? (
				<EmptyNote testId="agent-report-card-empty">
					<span className="flex flex-col items-center gap-3">
						<span>{t("reportCard.empty")}</span>
						<Button asChild size="sm">
							<Link to={"/evaluate" as never} search={{ agent: agentId } as never} data-testid="agent-report-card-evaluate">
								<Gauge aria-hidden="true" />
								{t("reportCard.evaluate")}
							</Link>
						</Button>
					</span>
				</EmptyNote>
			) : report.isError || !report.data ? (
				<LoadError title={t("reportCard.loadError")} error={report.error} onRetry={() => void report.refetch()} testId="agent-report-card-error" />
			) : (
				<>
					<ReportCard report={report.data} />
					<ReportFailures report={report.data} agent={agentId} />
				</>
			)}
		</div>
	);
}
