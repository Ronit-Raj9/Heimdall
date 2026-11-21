import { ExperimentTrialsPanel, hasContent } from "@/components/adaptive/trialsPanel"; // W7 adaptive trials and suite tiers
import { TERMINAL_STATES, systemExperiment } from "@/components/experiments/experiments.utils";
import { humanExperimentName } from "@/app/experiments/views/experimentBoard";
import { ExperimentExportMenu } from "@/components/export/exportMenu"; // W10 export
import { ExperimentRunActions, RegradeAction } from "@/components/platform/experimentActions";
import { RunList } from "@/components/runs/runList";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { PageHeader } from "@/components/shell/pageHeader";
import { RouteTabs, type RouteTab } from "@/components/shell/routeTabs";
import { StatusBadge } from "@/components/shell/statusBadge";
import { Button } from "@/components/ui/button";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { useAppDispatch } from "@/lib/store";
import { EXPERIMENT_POLL_MS, benchApi, experimentResultTags, useGetExperimentQuery, useGetGridQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useGetExperimentResearchQuery } from "@/lib/store/apis/researchApi";
import { useGetExperimentRoutingQuery } from "@/lib/store/apis/routingApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetExperimentSupportQuery } from "@/lib/store/apis/supportApi";
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { Link, useNavigate } from "@tanstack/react-router";
import { DeleteAction } from "@/components/platform/deleteAction";
import { useDeleteExperimentMutation } from "@/lib/store/apis/dataOpsApi";
import { Bot, Download, GitCompareArrows, Hourglass, SearchX } from "lucide-react";
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
		label: id === "actions" ? tActions("tab") : tPage(`tabs.${id}`),
		hidden: hidden.has(id) && !shownOnRequest(id),
		testId: `experiment-tab-${id}`,
	}));
	tabs.push({ id: ANALYSIS_TAB_ID, label: <AnalysisTabLabel />, testId: `experiment-tab-${ANALYSIS_TAB_ID}` });
	const decision = verdict.data?.verdict.decision;
	// Lead with a human title ("codex on coding-core", "Readiness check for acme-safety"); the raw id stays small below.
	const system = systemExperiment(st);
	const title = humanExperimentName({ ...st, arms }, (k, o) => String(tx(k as never, (o ?? {}) as never)));
	const bannerKey = system?.kind === "agent" ? "header.systemBannerAgent" : system?.kind === "drift" ? "header.systemBannerDrift" : "header.systemBanner";

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="experiment-screen" data-exp-id={expId}>
			<PageHeader
				breadcrumbs={[{ label: t("experiments.list.title"), to: "/experiments" }, { label: title }]}
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
						{decision && <StatusBadge decision={decision} testId="experiment-decision" />}
					</>
				}
				subtitle={
					<span className="flex flex-col gap-1">
						<span className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs" data-testid="experiment-facts">
							{st.suite && <EntityLink kind="suite" id={st.suite} />}
							{arms.length > 0 && !system && <span>{t("experiments.page.arms", { arms: arms.join(" vs "), count: arms.length })}</span>}
							<EntityLink kind="experiment" id={expId} className="text-[10px] opacity-50" />
						</span>
					</span>
				}
				actions={
					<div className="flex flex-wrap items-center gap-1.5" data-testid="experiment-actions">
						<ExperimentRunActions status={st} />
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

			{live && <LiveProgress status={st} />}

			<RouteTabs tabs={tabs} defaultTab={defaultExperimentTab(arms.length)} label={t("experiments.page.tabsLabel")} testId="experiment-tabs">
				{(active) =>
					active === ANALYSIS_TAB_ID ? (
						<AnalysisTab expId={expId} />
					) : notStarted && active === "verdict" ? (
						<EmptyState icon={Hourglass} message={t("experiments.page.notStarted")} testId="experiment-not-started" />
					) : (
						<ExperimentTab tab={active as ExperimentTabId} expId={expId} arms={arms} suite={st.suite} suiteVersion={st.suite_version} />
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

function ExperimentTab({ tab, expId, arms, suite, suiteVersion }: { tab: ExperimentTabId; expId: string; arms: readonly string[]; suite?: string; suiteVersion?: string }) {
	switch (tab) {
		case "verdict":
			return <VerdictTab expId={expId} />;
		case "runs":
			return <RunList expId={expId} testId="experiment-runs" />;
		case "matrix":
			return <MatrixTab expId={expId} />;
		case "cost":
			return <CostTab expId={expId} />;
		case "migration":
			return <MigrationTab expId={expId} arms={arms} />;
		case "adaptive":
			return <ExperimentTrialsPanel expId={expId} />;
		case "scorecards":
			return <ScorecardsTab expId={expId} />;
		case "receipts":
			return <ReceiptsTab expId={expId} suite={suite} />;
		case "integrity":
			return <IntegrityTab expId={expId} />;
		case "export":
			return <ExperimentExportMenu expId={expId} />;
		case "actions":
			return <ActionsTab expId={expId} suite={suite} suiteVersion={suiteVersion} />;
	}
}