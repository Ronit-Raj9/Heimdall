import { NativeSelect } from "@/components/ui/select";
import { ReportCard } from "@/components/reportcard/reportCard";
import { ReportFailures } from "@/components/reportcard/reportFailures";
import { ReportPicks } from "@/components/reportcard/reportPicks";
import "@/components/experiments/ufc2I18n";
import { ShareButton, shareQuery } from "@/components/share/shareDialog";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { formatWhen } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import { useGetAgentCardQuery, type AgentCard } from "@/lib/store/apis/agentCardApi";
import { preferredAgentEvaluation } from "@/components/home/agentEvaluation";
import { useGetAgentVersionsQuery, type ReportCardResponse } from "@/lib/store/apis/evaluationsApi";
import "@/components/agents/gw17I18n";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { Gauge } from "lucide-react";
import { useEffect } from "react";
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
 * The evaluation the tab should open on when the address names none (M3-2, Q-17): the server's newest pick, unless
 * that is a test object or a failure test; the same rule as Home's agent cards (preferredAgentEvaluation).
 * Undefined keeps the server's pick.
 */
export function preferredEvaluation(card: Pick<AgentCard, "evaluation_id" | "evaluation_name" | "evaluations" | "reliability">): string | undefined {
	return preferredAgentEvaluation(card);
}

/** Runs the card counts: every shown arm's runs over every benchmark. */
export function cardRuns(card: AgentCard): number {
	return Object.values(card.totals ?? {}).reduce((sum, row) => sum + (row?.runs ?? 0), 0);
}

/** The "Showing:" summary of the card (M2-34): "<evaluation>, <date> (n runs)". */
export function cardSummary(card: AgentCard, locale: string, runsText: (count: number) => string): string {
	const name = card.evaluation_name || card.evaluation_id || "";
	const date = formatWhen(card.evaluation_created_at, locale) ?? "";
	return [name, date].filter(Boolean).join(", ") + ` (${runsText(cardRuns(card))})`;
}

/**
 * The line that names the one evaluation the card shows and how many other done evaluations
 * match (N206): the card never mixes evaluations, so "Any model" is not all of the agent's runs.
 */
function CardSource({ card }: { card: AgentCard }) {
	const { t } = useTranslation("gw17");
	const locale = getLocale();
	if (!card.evaluation_id) return null;
	const list = card.evaluations ?? [];
	const matching = card.matching_evaluations ?? list.length;
	const newest = list.length === 0 || list[0].id === card.evaluation_id;
	const facts = {
		name: card.evaluation_name || card.evaluation_id,
		date: formatWhen(card.evaluation_created_at, locale) ?? "",
		runs: cardRuns(card),
	};
	const head =
		matching <= 1 ? t("reportCard.sourceOnly", facts) : newest ? t("reportCard.sourceNewest", facts) : t("reportCard.sourcePicked", facts);
	const others = matching - 1;
	const tail = others < 1 ? "" : newest ? t("reportCard.olderMatch", { count: others }) : t("reportCard.otherMatch", { count: others });
	return (
		<p className="text-muted-foreground basis-full text-xs" data-testid="agent-report-card-source">
			{tail ? `${head} ${tail}` : head}
		</p>
	);
}

/** The Evaluation select of the picks: one of the evaluations that match the version and model. */
function EvaluationSelect({ card, onPick }: { card: AgentCard; onPick: (id: string) => void }) {
	const { t } = useTranslation("gw17");
	const locale = getLocale();
	const list = card.evaluations ?? [];
	if (!card.evaluation_id || list.length <= 1) return null;
	return (
		<label className="flex min-w-0 flex-col gap-1 text-xs font-medium">
			{t("reportCard.evaluation")}
			<NativeSelect
				wrapperClassName="w-full max-w-md min-w-0"
				className="w-full"
				value={card.evaluation_id}
				onChange={(e) => onPick(e.target.value === list[0].id ? "" : e.target.value)}
				aria-label={t("reportCard.evaluation")}
				data-testid="agent-report-card-evaluation"
			>
				{list.map((ev) => (
					<option key={ev.id} value={ev.id}>
						{ev.name || ev.id}
						{ev.created_at ? ` · ${formatWhen(ev.created_at, locale) ?? ""}` : ""}
					</option>
				))}
			</NativeSelect>
		</label>
	);
}

/**
 * The Report card tab: pick a version and a model, see the latest report card for that pair, or pick one of the
 * matching evaluations. All three live in the address (?version=&model=&evaluation=), so a link from the
 * leaderboard opens on its row and a pick can be shared.
 */
export default function ReportCardTab({ agentId }: { agentId: string }) {
	const { t } = useTranslation("agentBuilder");
	const { t: tg } = useTranslation("gw17");
	const { t: tc } = useTranslation("ufc2");
	const search = useSearch({ strict: false }) as { version?: unknown; model?: unknown; evaluation?: unknown };
	const navigate = useNavigate();
	const versions = useGetAgentVersionsQuery(agentId);
	const versionList = [...(versions.data?.versions ?? [])].sort((x, y) => y.version - x.version);
	const urlVersion = /^v?[0-9]+$/.test(String(search.version ?? "")) ? Number(String(search.version).replace(/^v/, "")) : 0;
	// A version the agent does not have shows the latest, with a note, once the versions are known.
	const versionMissing = urlVersion > 0 && versions.data !== undefined && !versionList.some((v) => v.version === urlVersion);
	const version = versionMissing ? 0 : urlVersion;
	const model = typeof search.model === "string" ? search.model : "";
	const evaluation = typeof search.evaluation === "string" ? search.evaluation : "";
	// A new version or model starts again from the newest matching evaluation.
	const setFilter = (next: { version?: number; model?: string; evaluation?: string }) =>
		void navigate({
			to: ".",
			search: ((prev: Record<string, unknown>) => ({
				...prev,
				version: (next.version ?? version) || undefined,
				model: (next.model ?? model) || undefined,
				evaluation: next.evaluation || undefined,
			})) as never,
			replace: true,
		});
	const report = useGetAgentCardQuery({
		agent: agentId,
		version: version || undefined,
		model: model || undefined,
		evaluation: evaluation || undefined,
	});
	const modelList = modelOptions(report.data?.models, model);
	// Open on a score evaluation, not a failure test, when the address names none (M3-2).
	const preferred = !evaluation && report.data ? preferredEvaluation(report.data) : undefined;
	useEffect(() => {
		if (preferred) setFilter({ evaluation: preferred });
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [preferred]);
	const empty = isEmptyReport(report.data, report.error);
	const filterSummary = `${version ? `v${version}` : t("reportCard.latest")}, ${model || t("reportCard.anyModel")}`;
	const picksSummary =
		report.data && !empty && report.data.evaluation_id
			? cardSummary(report.data, getLocale(), (count) => tc("picks.runs", { count }))
			: filterSummary;

	return (
		<div className="flex min-w-0 flex-col gap-5" data-testid="agent-report-card-tab">
			{/* One "Showing: ... Change" line; the Version, Model and Evaluation selects open under Change (M2-34). */}
			<ReportPicks summary={picksSummary} testId="agent-report-card-picks">
				<label className="flex flex-col gap-1.5 text-xs font-medium">
					{t("reportCard.version")}
					<NativeSelect
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
					</NativeSelect>
				</label>
				<label className="flex flex-col gap-1.5 text-xs font-medium">
					{t("reportCard.model")}
					<NativeSelect
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
					</NativeSelect>
				</label>
				{report.data && !empty && <EvaluationSelect card={report.data} onPick={(id) => setFilter({ evaluation: id })} />}
				{report.data && !empty && <CardSource card={report.data} />}
			</ReportPicks>
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