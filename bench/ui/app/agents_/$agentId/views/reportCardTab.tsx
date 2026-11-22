import { ReportCard } from "@/components/reportcard/reportCard";
import { ReportFailures } from "@/components/reportcard/reportFailures";
import { ShareButton, shareQuery } from "@/components/share/shareDialog";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { useGetAgentReportCardQuery, useGetAgentVersionsQuery, type ReportCardResponse } from "@/lib/store/apis/evaluationsApi";
import "@/components/agents/gw17I18n";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Gauge } from "lucide-react";
import { useTranslation } from "react-i18next";

/** True when the server had no completed evaluation for this agent, version and model. */
export function isEmptyReport(report: ReportCardResponse | undefined, error: unknown): boolean {
	if (error && typeof error === "object" && "status" in error && (error as { status: unknown }).status === 404) return true;
	if (!report) return false;
	return !report.evaluation || ((report.capabilities ?? []).length === 0 && (report.benchmarks ?? []).length === 0);
}

/**
 * The Model filter's choices: the models this agent's evaluations used (the server's `models`), with the selected
 * model first when the list lacks it (a link can name a model the list does not have yet).
 */
export function modelOptions(models: readonly string[] | undefined, selected: string): string[] {
	const list = [...(models ?? [])];
	return selected && !list.includes(selected) ? [selected, ...list] : list;
}

/**
 * The Report card tab: pick a version and a model, see the latest report card for that pair. Both live in the
 * address (?version=&model=), so a link from the leaderboard opens on its row and a pick can be shared.
 */
export default function ReportCardTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tg } = useTranslation("gw17");
	const search = useSearch({ strict: false }) as { version?: unknown; model?: unknown };
	const navigate = useNavigate();
	const versions = useGetAgentVersionsQuery(agentId);
	const versionList = [...(versions.data?.versions ?? [])].sort((x, y) => y.version - x.version);
	const urlVersion = /^v?[0-9]+$/.test(String(search.version ?? "")) ? Number(String(search.version).replace(/^v/, "")) : 0;
	// A version the agent does not have shows the latest, with a note, once the versions are known.
	const versionMissing = urlVersion > 0 && versions.data !== undefined && !versionList.some((v) => v.version === urlVersion);
	const version = versionMissing ? 0 : urlVersion;
	const model = typeof search.model === "string" ? search.model : "";
	const setFilter = (next: { version?: number; model?: string }) =>
		void navigate({
			to: ".",
			search: ((prev: Record<string, unknown>) => ({
				...prev,
				version: (next.version ?? version) || undefined,
				model: (next.model ?? model) || undefined,
			})) as never,
			replace: true,
		});
	const report = useGetAgentReportCardQuery({ agent: agentId, version: version || undefined, model: model || undefined });
	const modelList = modelOptions(report.data?.models, model);
	const selectClass = "border-input bg-background h-9 rounded-md border px-2 text-sm";
	const empty = isEmptyReport(report.data, report.error);

	return (
		<div className="flex min-w-0 flex-col gap-5" data-testid="agent-report-card-tab">
			<div className="flex flex-wrap items-end gap-3">
				<label className="flex flex-col gap-1 text-xs font-medium">
					{t("reportCard.version")}
					<select
						className={selectClass}
						value={version}
						onChange={(e) => setFilter({ version: Number(e.target.value) })}
						aria-label={t("reportCard.version")}
						data-testid="agent-report-card-version"
					>
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
					<select
						className={selectClass}
						value={model}
						onChange={(e) => setFilter({ model: e.target.value })}
						disabled={modelList.length === 0}
						aria-label={t("reportCard.model")}
						data-testid="agent-report-card-model"
					>
						<option value="">{t("reportCard.anyModel")}</option>
						{modelList.map((id) => (
							<option key={id} value={id}>
								{id}
							</option>
						))}
					</select>
				</label>
			</div>
			{versionMissing && (
				<p className="text-muted-foreground text-xs" data-testid="agent-report-card-version-missing">
					{tg("reportCard.versionMissing", { version: urlVersion })}
				</p>
			)}
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
				<LoadError
					title={t("reportCard.loadError")}
					error={report.error}
					onRetry={() => void report.refetch()}
					testId="agent-report-card-error"
				/>
			) : (
				<>
					<ReportCard
						report={report.data}
						actions={
							<ShareButton
								kind="report_card"
								target={agentId}
								query={shareQuery({ version: version || undefined, model: model || undefined })}
							/>
						}
					/>
					<ReportFailures report={report.data} agent={agentId} />
				</>
			)}
		</div>
	);
}