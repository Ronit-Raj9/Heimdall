import { ExperimentTrialsPanel, hasContent } from "@/components/adaptive/trialsPanel"; // W7 adaptive trials and suite tiers
import { ExperimentProgressCard } from "@/components/experiments/experimentProgress";
import { TERMINAL_STATES } from "@/components/experiments/experiments.utils";
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
import { Card, CardContent } from "@/components/ui/card";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { EXPERIMENT_POLL_MS, useGetExperimentQuery, useGetGridQuery, useGetVerdictQuery } from "@/lib/store/apis/benchApi";
import { useGetExperimentResearchQuery } from "@/lib/store/apis/researchApi";
import { useGetExperimentRoutingQuery } from "@/lib/store/apis/routingApi";
import { errorStatus } from "@/lib/store/apis/session";
import { useGetExperimentSupportQuery } from "@/lib/store/apis/supportApi";
import { compareSearch } from "@/components/experiments/legacyRoutes";
import { Link } from "@tanstack/react-router";
import { GitCompareArrows, Hourglass, SearchX } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import { CostTab } from "./costTab";
import { EXPERIMENT_TABS, hiddenExperimentTabs, type ExperimentTabId } from "./experimentTabs";
import { IntegrityTab } from "./integrityTab";
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
	const [grade] = useQueryState("grade", parseAsString);
	const [tabParam] = useQueryState("tab", parseAsString);
	const status = useGetExperimentQuery(expId);
	const live = status.data ? !TERMINAL_STATES.has(status.data.state) && status.data.state !== "created" : false;
	// Poll while it runs, so the header, the progress card and the runs tab stay current.
	useGetExperimentQuery(expId, { pollingInterval: live ? EXPERIMENT_POLL_MS : 0, skipPollingIfUnfocused: true, skip: !live });
	// An experiment that has not started has no runs to analyse, so nothing asks for its verdict.
	const notStarted = status.data?.state === "created";
	const verdict = useGetVerdictQuery(grade ? { expId, gradeSource: grade } : expId, { skip: !status.data || notStarted });
	const grid = useGetGridQuery(expId, { skip: !status.data });
	// The facts that decide the tabs. Each is the request its tab makes anyway, so nothing is fetched twice.
	const adaptive = useGetExperimentAdaptiveQuery(expId, { skip: !status.data });
	const support = useGetExperimentSupportQuery(expId, { skip: !status.data });
	const research = useGetExperimentResearchQuery(expId, { skip: !status.data });
	const routing = useGetExperimentRoutingQuery(expId, { skip: !status.data });

	if (status.isLoading) return <BlockSkeleton rows={8} testId="experiment-loading" />;
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
		label: t(`experiments.page.tabs.${id}`),
		hidden: hidden.has(id) && !shownOnRequest(id),
		testId: `experiment-tab-${id}`,
	}));
	const decision = verdict.data?.verdict.decision;

	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="experiment-screen" data-exp-id={expId}>
			<PageHeader
				breadcrumbs={[{ label: t("experiments.list.title"), to: "/experiments" }, { label: st.name || expId }]}
				title={st.name || expId}
				meta={
					<>
						<StatusBadge state={st.state} testId="experiment-state" />
						{decision && <StatusBadge decision={decision} testId="experiment-decision" />}
					</>
				}
				subtitle={
					<span className="flex flex-wrap items-center gap-x-3 gap-y-1" data-testid="experiment-facts">
						<EntityLink kind="experiment" id={expId} className="text-xs" />
						{st.suite && (
							<span className="text-xs">
								{t("experiments.page.suite")} <EntityLink kind="suite" id={st.suite} />
							</span>
						)}
						{arms.length > 0 && (
							<span className="text-xs">{t("experiments.page.arms", { arms: arms.join(", "), count: arms.length })}</span>
						)}
					</span>
				}
				actions={
					<>
						<ExperimentRunActions status={st} />
						<RegradeAction expId={expId} />
						<Button variant="outline" size="sm" asChild>
							<Link to="/compare" search={compareSearch(undefined, expId) as never} data-testid="experiment-compare">
								<GitCompareArrows aria-hidden="true" />
								{t("experiments.page.compare")}
							</Link>
						</Button>
					</>
				}
			/>

			{live && (
				<Card className="py-4 shadow-none" data-testid="experiment-progress">
					<CardContent className="px-4">
						<ExperimentProgressCard status={st} />
					</CardContent>
				</Card>
			)}

			<RouteTabs tabs={tabs} defaultTab="verdict" label={t("experiments.page.tabsLabel")} testId="experiment-tabs">
				{(active) =>
					notStarted && active === "verdict" ? (
						<EmptyState icon={Hourglass} message={t("experiments.page.notStarted")} testId="experiment-not-started" />
					) : (
						<ExperimentTab tab={active as ExperimentTabId} expId={expId} arms={arms} suite={st.suite} />
					)
				}
			</RouteTabs>
		</div>
	);
}

function ExperimentTab({ tab, expId, arms, suite }: { tab: ExperimentTabId; expId: string; arms: readonly string[]; suite?: string }) {
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
	}
}