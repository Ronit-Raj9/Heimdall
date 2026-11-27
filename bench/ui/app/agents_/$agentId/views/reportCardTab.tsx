import { ReportCard } from "@/components/reportcard/reportCard";
import { ReportFailures } from "@/components/reportcard/reportFailures";
import { ShareButton, shareQuery } from "@/components/share/shareDialog";
import { BlockSkeleton, EmptyNote, LoadError } from "@/components/runs/screenStates";
import { Button } from "@/components/ui/button";
import { formatWhen } from "@/components/platform/platform.utils";
import { getLocale } from "@/lib/i18n";
import { useGetAgentCardQuery, type AgentCard } from "@/lib/store/apis/agentCardApi";
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

/** True when an evaluation entry reads as a failure test (its saved name starts so; the list has no mode yet). */
function looksLikeFailureTest(ev: { name?: string }): boolean {
	return /^failure test/i.test(ev.name ?? "");
}

/**
 * The evaluation the tab should open on when the address names none (M3-2): the server's newest pick, unless that is a
 * failure test and a plain score evaluation exists, then the newest plain one. Undefined keeps the server's pick.
 */
export function preferredEvaluation(card: Pick<AgentCard, "evaluation_id" | "evaluations" | "reliability">): string | undefined {
	const list = card.evaluations ?? [];
	const shown = list.find((e) => e.id === card.evaluation_id);
	const shownIsFault = (shown && looksLikeFailureTest(shown)) || (card.reliability ?? []).some((r) => r.fault !== "clean");
	if (!shownIsFault) return undefined;
	const plain = list.find((e) => e.id !== card.evaluation_id && !looksLikeFailureTest(e));
	return plain?.id;
}

/** Runs the card counts: every shown arm's runs over every benchmark. */
export function cardRuns(card: AgentCard): number {
	return Object.values(card.totals ?? {}).reduce((sum, row) => sum + (row?.runs ?? 0), 0);
}

/**
 * The line above the card that names the one evaluation it shows and how many other done evaluations
 * match (N206): the card never mixes evaluations, so "Any model" is not all of the agent's runs.
 */
function CardSource({ card, onPick }: { card: AgentCard; onPick: (id: string) => void }) {
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
		<div className="flex flex-col gap-2" data-testid="agent-report-card-source">
			<p className="text-muted-foreground text-xs">{tail ? `${head} ${tail}` : head}</p>
			{list.length > 1 && (
				<label className="flex flex-col gap-1 text-xs font-medium">
					{t("reportCard.evaluation")}
					<select
						className="border-input bg-background h-9 max-w-md rounded-md border px-2 text-sm"
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
					</select>
				</label>
			)}
		</div>
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
					<CardSource card={report.data} onPick={(id) => setFilter({ evaluation: id })} />
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