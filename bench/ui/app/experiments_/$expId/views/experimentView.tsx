import { ExperimentTrialsPanel, hasContent } from "@/components/adaptive/trialsPanel"; // W7 adaptive trials and suite tiers
import {
	TERMINAL_STATES,
	experimentKind,
	humanExperimentName,
	suiteName,
	systemExperiment,
	type ExperimentKind,
} from "@/components/experiments/experiments.utils";
import { useDriftMonitorNames } from "@/components/experiments/useDriftMonitorNames";
import { ExperimentExportMenu } from "@/components/export/exportMenu"; // W10 export
import { ExperimentRunActions, RegradeAction } from "@/components/platform/experimentActions";
import { RunControlsPanel } from "@/components/experiments/runControls"; // run control
import { RunList } from "@/components/runs/runList";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { ShareButton } from "@/components/share/shareDialog";
import { PageHeader } from "@/components/shell/pageHeader";
import { RouteTabs, type RouteTab } from "@/components/shell/routeTabs";
import { StatusBadge } from "@/components/shell/statusBadge";
import { Button } from "@/components/ui/button";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { useAppDispatch } from "@/lib/store";
import {
	EXPERIMENT_POLL_MS,
	benchApi,
	experimentResultTags,
	useGetExperimentQuery,
	useGetGridQuery,
	useGetVerdictQuery,
	useListRunsQuery,
} from "@/lib/store/apis/benchApi";
import { useGetEvaluationQuery } from "@/lib/store/apis/evaluationsApi";
import type { ExperimentStatus } from "@/lib/types/bench";
import { useGetExperimentResearchQuery } from "@/lib/store/apis/researchApi";
import { useGetExperimentRoutingQuery } from "@/lib/store/apis/routingApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetExperimentSupportQuery } from "@/lib/store/apis/supportApi";
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { Link, useNavigate } from "@tanstack/react-router";
import { DeleteAction } from "@/components/platform/deleteAction";
import { useDeleteExperimentMutation } from "@/lib/store/apis/dataOpsApi";
import { Bot, CornerUpLeft, Download, GitCompareArrows, Hourglass, Layers, ListChecks, SearchX } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { ActionsTab } from "./actionsTab";
import { ANALYSIS_TAB_ID, AnalysisTab, AnalysisTabLabel } from "./analysisTab"; // L15 analysis jobs
import { CostTab } from "./costTab";
import { EXPERIMENT_TABS, defaultExperimentTab, hiddenExperimentTabs, type ExperimentTabId } from "./experimentTabs";
import { IntegrityTab } from "./integrityTab";
import { LiveProgress } from "./liveProgress";
import { suiteIcon } from "./suiteIcon";
import { MatrixTab } from "./matrixTab";
import { MigrationTab } from "./migrationTab";
import { ReceiptsTab } from "./receiptsTab";
import { ScorecardsTab } from "./scorecardsTab";
import { VerdictTab } from "./verdictTab";
import "./fw11I18n";
import "./hw12I18n";

/**
 * Status and verdict facts other lanes add in fix wave 3, read through local types (D15): the comparison a
 * follow-up extends (`extends`) and whether a verdict was computed while runs were still going (`partial`).
 */
type StatusFacts = ExperimentStatus & { extends?: string };
type VerdictFacts = { partial?: boolean };

/** The header's tag for what a check or a debug run is, shown instead of a verdict (N253, N247). */
function HeaderTag({ children, testId, title }: { children: React.ReactNode; testId: string; title?: string }) {
	return (
		<span
			className="border-border bg-muted/50 text-muted-foreground inline-flex h-5 shrink-0 items-center gap-1 rounded-[5px] border px-1.5 text-xs leading-4 font-medium whitespace-nowrap"
			data-testid={testId}
			title={title}
		>
			{children}
		</span>
	);
}

/** "Solved" or "Not solved" for a debug run's single sealed run, else null. Pure. */
export function debugOutcome(runs: readonly { outcome?: string; sealed?: boolean }[] | undefined): "solved" | "notSolved" | null {
	if (!runs || runs.length !== 1 || !runs[0].sealed) return null;
	return runs[0].outcome === "pass" ? "solved" : "notSolved";
}

/** Where a readiness check's ready tasks are listed: the benchmark's Build tab, or Add benchmark for a draft. */
function readyTasksLink(
	suite: string | undefined,
): { to: string; params?: Record<string, string>; search?: Record<string, string> } | null {
	const id = suiteName(suite);
	if (!id) return null;
	return id.startsWith("draft-") ? { to: "/suites/add" } : { to: "/suites/$suiteId", params: { suiteId: id }, search: { tab: "build" } };
}

/**
 * /experiments/$expId: one experiment. The header carries its status, verdict and actions; the tabs
 * hold every view of it (?tab=), and tabs that do not apply to this experiment are hidden.
 */
export default function ExperimentView({ expId }: { expId: string }) {
	const { t } = useTranslation("bench");
	const { t: tActions } = useTranslation("experimentActions");
	const [grade] = useQueryState("grade", parseAsString);
	const { t: tPage } = useTranslation("experimentPage");
	const { t: tx } = useTranslation("experimentsUx");
	const { t: tf } = useTranslation("fw11");
	const { t: th } = useTranslation("hw12");
	const [tabParam, setTab] = useQueryState("tab", parseAsString);
	const status = useGetExperimentQuery(expId);
	const live = status.data ? !TERMINAL_STATES.has(status.data.state) && status.data.state !== "created" : false;
	// Poll while it runs, so the header, the progress card and the runs tab stay current.
	useGetExperimentQuery(expId, { pollingInterval: live ? EXPERIMENT_POLL_MS : 0, skipPollingIfUnfocused: true, skip: !live });
	// An experiment that has not started has no runs to analyse, so nothing asks for its verdict.
	const notStarted = status.data?.state === "created";
	const verdict = useGetVerdictQuery(grade ? { expId, gradeSource: grade } : expId, { skip: !status.data || notStarted });
	// The grid fills in while the experiment runs, so it polls alongside the status.
	const grid = useGetGridQuery(expId, { skip: !status.data, pollingInterval: live ? EXPERIMENT_POLL_MS : 0, skipPollingIfUnfocused: true });
	// When a running experiment stops, its verdict, diagnostics, grid and runs were computed from a
	// partial experiment: refresh them once.
	const dispatch = useAppDispatch();
	const wasLive = useRef(live);
	useEffect(() => {
		if (wasLive.current && !live) dispatch(benchApi.util.invalidateTags(experimentResultTags(expId)));
		wasLive.current = live;
	}, [live, expId, dispatch]);
	// The facts that decide the tabs. Each is the request its tab makes anyway, so nothing is fetched twice.
	const adaptive = useGetExperimentAdaptiveQuery(expId, { skip: !status.data });
	const support = useGetExperimentSupportQuery(expId, { skip: !status.data });
	const research = useGetExperimentResearchQuery(expId, { skip: !status.data });
	const routing = useGetExperimentRoutingQuery(expId, { skip: !status.data });
	// Fix wave 3 header facts: the evaluation that made it (N203), the comparison it extends (N246), the run of a
	// debug experiment (N247) and the drift monitors that still exist (N257).
	const current = status.data && status.data.exp_id === expId ? (status.data as StatusFacts) : undefined;
	const parentId = current?.extends ?? "";
	const parent = useGetExperimentQuery(parentId, { skip: !parentId });
	const evaluationOf = useGetEvaluationQuery(current?.evaluation_id ?? "", { skip: !current?.evaluation_id });
	const kind: ExperimentKind | null = current ? experimentKind(current) : null;
	const debugRuns = useListRunsQuery({ exp: expId }, { skip: kind !== "debug" });
	const monitors = useDriftMonitorNames();

	// RTK keeps the last result in data while a new argument loads; only a response for this id may
	// drive the header and its write actions (QC2-28-04).
	if (status.isLoading || (status.data && status.data.exp_id !== expId)) return <BlockSkeleton rows={8} testId="experiment-loading" />;
	if (status.error || !status.data) {
		if (errorStatus(status.error) === 404) {
			return (
				<EmptyState
					icon={SearchX}
					message={t("experiments.page.notFound", { id: expId })}
					action={{ label: t("experiments.page.backToList"), to: "/experiments" }}
					testId="experiment-not-found"
				/>
			);
		}
		return (
			<LoadError
				title={t("experiments.page.loadError")}
				error={status.error}
				onRetry={() => void status.refetch()}
				testId="experiment-error"
			/>
		);
	}

	const st = status.data;
	const arms = st.arms ?? grid.data?.arms ?? [];
	const hidden = hiddenExperimentTabs({
		arms: arms.length,
		adaptive: Boolean(adaptive.data && hasContent(adaptive.data)),
		scorecards: Boolean(support.data?.support || research.data?.research || routing.data?.routed),
		probes: Boolean(grid.data?.conditions.some((condition) => condition !== "clean")),
	});
	// A two-arm matrix repeats the verdict, so it is hidden, but an old /matrix?exp= link that asks for it still gets it.
	const shownOnRequest = (id: ExperimentTabId) => id === "matrix" && tabParam === "matrix" && arms.length >= 2;
	const tabs: RouteTab[] = EXPERIMENT_TABS.map((id) => ({
		id,
		// One setup is scored on its own, so its result tab reads "Score", not a comparison.
		label: id === "actions" ? tActions("tab") : id === "verdict" && arms.length === 1 ? tPage("tabs.verdictScore") : tPage(`tabs.${id}`),
		hidden: hidden.has(id) && !shownOnRequest(id),
		testId: `experiment-tab-${id}`,
	}));
	tabs.push({ id: ANALYSIS_TAB_ID, label: <AnalysisTabLabel />, testId: `experiment-tab-${ANALYSIS_TAB_ID}` });
	const decision = verdict.data?.verdict.decision;
	// A verdict worked out while runs are still going is not final (N197): no decision until the end.
	const partial = live || Boolean((verdict.data as VerdictFacts | undefined)?.partial);
	// Lead with a human title ("codex on coding-core", "Readiness check for acme-safety"); the raw id stays small below.
	const system = systemExperiment(st);
	// The evaluation that made this experiment (GET /experiments/{id} carries it once the server sends it).
	const evaluationId = st.evaluation_id || undefined;
	const evaluationName =
		evaluationId && evaluationOf.data?.id === evaluationId ? evaluationOf.data.spec?.name?.trim() || evaluationId : undefined;
	const evalNames = evaluationId && evaluationName ? new Map([[evaluationId, evaluationName]]) : undefined;
	const nameOf = (k: string, o?: Record<string, string>) => String(tx(k as never, (o ?? {}) as never));
	const title = humanExperimentName({ ...st, arms }, nameOf, evalNames, monitors);
	const parentStatus = parentId && parent.data?.exp_id === parentId ? parent.data : undefined;
	const parentTitle = parentStatus ? humanExperimentName(parentStatus, nameOf, undefined, monitors) : parentId;
	const outcome = kind === "debug" ? debugOutcome(debugRuns.data?.runs) : null;
	const readyLink = kind === "readiness" ? readyTasksLink(st.suite) : null;
	const bannerKey =
		system?.kind === "agent" ? "header.systemBannerAgent" : system?.kind === "drift" ? "header.systemBannerDrift" : "header.systemBanner";

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="experiment-screen" data-exp-id={expId}>
			<PageHeader
				breadcrumbs={[
					{ label: t("experiments.list.title"), to: "/experiments" },
					...(evaluationId
						? [
								{
									label: th("header.evaluationCrumb", { name: evaluationName ?? evaluationId }),
									to: "/evaluations/$evalId",
									params: { evalId: evaluationId },
								},
							]
						: []),
					{ label: title },
				]}
				title={
					<span className="flex min-w-0 items-center gap-2.5">
						<SuiteGlyph suite={st.suite} />
						<span className="truncate" title={expId}>
							{title}
						</span>
					</span>
				}
				meta={
					<>
						<StatusBadge state={st.state} testId="experiment-state" />
						{kind ? (
							<>
								<HeaderTag testId="experiment-kind">{tx(`kind.${kind}`)}</HeaderTag>
								{outcome && <HeaderTag testId="experiment-debug-outcome">{th(`header.${outcome}`)}</HeaderTag>}
							</>
						) : partial ? (
							decision && (
								<HeaderTag testId="experiment-partial" title={th("header.partialHint")}>
									{th("header.partial")}
								</HeaderTag>
							)
						) : (
							decision && <StatusBadge decision={decision} testId="experiment-decision" />
						)}
					</>
				}
				subtitle={
					<span className="flex flex-col gap-1">
						<span className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs" data-testid="experiment-facts">
							{st.suite && <EntityLink kind="suite" id={st.suite} />}
							{arms.length > 0 && !system && <span>{t("experiments.page.arms", { arms: arms.join(" vs "), count: arms.length })}</span>}
							{parentId && (
								<span className="inline-flex items-center gap-1" data-testid="experiment-follow-up">
									<CornerUpLeft className="size-3" aria-hidden="true" />
									{th("header.followUp")}
									<Link to="/experiments/$expId" params={{ expId: parentId }} className="text-primary font-medium hover:underline">
										{parentTitle}
									</Link>
								</span>
							)}
							{readyLink && (
								<Link
									to={readyLink.to as never}
									params={readyLink.params as never}
									search={readyLink.search as never}
									className="text-primary inline-flex items-center gap-1 font-medium hover:underline"
									data-testid="experiment-ready-tasks"
								>
									<ListChecks className="size-3" aria-hidden="true" />
									{th("header.readyTasks")}
								</Link>
							)}
							<EntityLink kind="experiment" id={expId} className="text-[10px] opacity-50" />
						</span>
					</span>
				}
				actions={
					<div className="flex flex-wrap items-center gap-1.5" data-testid="experiment-actions">
						<ExperimentRunActions status={st} />
						<ShareButton kind="verdict" target={expId} />
						<div className="bg-muted/40 flex items-center gap-0.5 rounded-lg border p-0.5">
							<RegradeAction expId={expId} />
							<Button variant="ghost" size="sm" asChild>
								<Link to="/compare" search={compareSearch(undefined, expId) as never} data-testid="experiment-compare">
									<GitCompareArrows aria-hidden="true" />
									{t("experiments.page.compare")}
								</Link>
							</Button>
							<Button variant="ghost" size="sm" onClick={() => void setTab("export")} dataTestId="experiment-export">
								<Download aria-hidden="true" />
								{tPage("header.export")}
							</Button>
						</div>
						<DeleteExperimentAction expId={expId} running={live} />
					</div>
				}
			/>

			{system && (
				<p
					className="bg-muted/60 text-foreground flex items-start gap-2 rounded-lg border px-4 py-3 text-sm"
					role="note"
					data-testid="experiment-system-banner"
				>
					<Bot className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{tPage(bannerKey)}
				</p>
			)}

			{evaluationId && arms.length === 1 && (
				<p
					className="bg-muted/60 text-foreground flex flex-wrap items-start gap-2 rounded-lg border px-4 py-3 text-sm"
					role="note"
					data-testid="experiment-evaluation-note"
				>
					<Layers className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden="true" />
					<span className="min-w-0 flex-1">{tf("page.evaluationNote")}</span>
					<Link
						to="/evaluations/$evalId"
						params={{ evalId: evaluationId }}
						className="text-primary font-medium hover:underline"
						data-testid="experiment-evaluation-link"
					>
						{tf("page.openEvaluation")}
					</Link>
				</p>
			)}

			{live && <LiveProgress status={st} />}

			{!notStarted && <RunControlsPanel expId={expId} state={st.state} />}

			<RouteTabs
				tabs={tabs}
				defaultTab={defaultExperimentTab(arms.length)}
				label={t("experiments.page.tabsLabel")}
				testId="experiment-tabs"
			>
				{(active) =>
					active === ANALYSIS_TAB_ID ? (
						<AnalysisTab expId={expId} />
					) : notStarted && active === "verdict" ? (
						<EmptyState icon={Hourglass} message={t("experiments.page.notStarted")} testId="experiment-not-started" />
					) : (
						<ExperimentTab
							tab={active as ExperimentTabId}
							expId={expId}
							arms={arms}
							suite={st.suite}
							suiteVersion={st.suite_version}
							evaluationId={evaluationId}
						/>
					)
				}
			</RouteTabs>
		</div>
	);
}

/** Delete (admin): confirm, delete, back to the list. Disabled while the comparison runs. */
function DeleteExperimentAction({ expId, running }: { expId: string; running: boolean }) {
	const { t } = useTranslation("experimentPage");
	const navigate = useNavigate();
	const [remove] = useDeleteExperimentMutation();
	return (
		<DeleteAction
			label={t("header.delete")}
			confirmTitle={t("header.deleteConfirmTitle")}
			confirmBody={t("header.deleteConfirm")}
			confirmLabel={t("header.deleteConfirmOk")}
			cancelLabel={t("header.deleteKeep")}
			busyReason={running ? t("header.deleteBusy") : undefined}
			adminOnly={t("header.deleteAdminOnly")}
			errorText={(error) => t("header.deleteError", { error })}
			onDelete={() => remove(expId).unwrap()}
			onDeleted={() => void navigate({ to: "/experiments" })}
			testId="experiment-delete"
		/>
	);
}

function SuiteGlyph({ suite }: { suite?: string }) {
	const Icon = suiteIcon(suite);
	return (
		<span className="text-muted-foreground flex shrink-0 items-center" aria-hidden="true">
			<Icon className="size-4" />
		</span>
	);
}

function ExperimentTab({
	tab,
	expId,
	arms,
	suite,
	suiteVersion,
	evaluationId,
}: {
	tab: ExperimentTabId;
	expId: string;
	arms: readonly string[];
	suite?: string;
	suiteVersion?: string;
	evaluationId?: string;
}) {
	switch (tab) {
		case "verdict":
			return <VerdictTab expId={expId} evaluationId={evaluationId} />;
		case "runs":
			return <RunList expId={expId} testId="experiment-runs" />;
		case "matrix":
			return <MatrixTab expId={expId} />;
		case "cost":
			return <CostTab expId={expId} arms={arms} />;
		case "migration":
			return <MigrationTab expId={expId} arms={arms} evaluationId={evaluationId} />;
		case "adaptive":
			return <ExperimentTrialsPanel expId={expId} />;
		case "scorecards":
			return <ScorecardsTab expId={expId} />;
		case "receipts":
			return <ReceiptsTab expId={expId} suite={suite} />;
		case "integrity":
			return <IntegrityTab expId={expId} />;
		case "export":
			return <ExportTab expId={expId} />;
		case "actions":
			return <ActionsTab expId={expId} suite={suite} suiteVersion={suiteVersion} />;
	}
}

/** The Export tab: one line on what the downloads are, then the export menu open (N70). */
function ExportTab({ expId }: { expId: string }) {
	const { t } = useTranslation("experimentPage");
	return (
		<div className="flex min-w-0 flex-col gap-3" data-testid="experiment-export-tab">
			<p className="text-muted-foreground text-sm">{t("exportTab.description")}</p>
			<ExperimentExportMenu expId={expId} initiallyOpen />
		</div>
	);
}