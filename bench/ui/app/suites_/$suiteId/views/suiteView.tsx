import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { GradingBadge, TrackBadge } from "@/components/scenario/scenarioBadges";
import { suiteGrading, suiteTrack } from "@/components/scenario/scenario.utils";
import { EmptyState, PageHeader, RouteTabs, suiteIdOf, type RouteTab } from "@/components/shell";
import { suiteVersionOf } from "@/components/shell/entityLink";
import { LockBadge } from "@/components/suites/suiteBadges";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "@/components/suites/suiteFacts";
import { FingerprintDriftNotice } from "@/components/suites/suiteLockKind";
import { healthState } from "@/components/suites/suiteHealth.utils";
import { leaderboardSuite } from "@/components/suites/suites.utils";
import { Button } from "@/components/ui/button";
import { useGetSuiteHealthQuery, useGetSuitesQuery } from "@/lib/store";
import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, ChevronRight, Trophy } from "lucide-react";
import { useTranslation } from "react-i18next";
import { SuiteCalibrationTab } from "./calibrationTab";
import { OpsTab } from "./opsTab";
import { SuiteExportTab, SuiteHealthTab, SuiteTiersTab } from "./suiteTabs";
import { SuiteTasksTab } from "./tasksTab";

/**
 * One suite: /suites/$suiteId?tab=. Tasks (default, with the task table's filters and ?task= for
 * the open task), Health, Tiers, Calibration (judged suites only) and Import and export.
 */
export default function SuiteView({ suiteId }: { suiteId: string }) {
	const { t } = useTranslation("bench");
	const query = useGetSuitesQuery();
	const data = query.data;
	// A suite reference may carry its version ("coding-core@1.0.0"), as verdicts and runs do.
	const suite = data?.suites.find((s) => s.id === suiteId || s.id === suiteIdOf(suiteId)) ?? null;
	const crumbs = [{ label: t("suites.title"), to: "/suites" }, { label: suiteId }];

	if (query.isLoading) {
		return (
			<section className="flex min-w-0 flex-col gap-6" data-testid="suite-screen">
				<PageHeader title={suiteId} breadcrumbs={crumbs} />
				<BlockSkeleton rows={6} testId="suite-loading" />
			</section>
		);
	}
	if (query.isError || !data) {
		return (
			<section className="flex min-w-0 flex-col gap-6" data-testid="suite-screen">
				<PageHeader title={suiteId} breadcrumbs={crumbs} />
				<LoadError title={t("suites.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="suite-error" />
			</section>
		);
	}
	if (!suite) {
		return (
			<section className="flex min-w-0 flex-col gap-6" data-testid="suite-screen">
				<PageHeader title={suiteId} breadcrumbs={crumbs} />
				<EmptyState
					icon={Boxes}
					message={t("suites.detail.notFound", { suite: suiteId })}
					action={{ label: t("suites.detail.allSuites"), to: "/suites" }}
					testId="suite-not-found"
				/>
			</section>
		);
	}
	return <SuiteBody suite={suite} mode={data.mode} crumbs={crumbs} requestedVersion={suiteVersionOf(suiteId)} />;
}

function SuiteBody({
	suite,
	mode,
	crumbs,
	requestedVersion,
}: {
	suite: SuiteSummary;
	mode: string;
	crumbs: { label: string; to?: string }[];
	/** The version a link asked for (an experiment's frozen suite), when it named one. */
	requestedVersion?: string | null;
}) {
	const { t } = useTranslation("bench");
	// Health is a separate request: a failure or a 503 (no run store) shows "health unavailable" and never blocks the page.
	const healthQuery = useGetSuiteHealthQuery(suite.id, { skip: Boolean(suite.error) });
	const health = healthState(healthQuery);
	const judged = suiteGrading(suite) === "judged";
	const tasks = suite.tasks.length;
	const tabs: RouteTab[] = [
		{ id: "tasks", label: t("suites.tabs.tasks"), badge: tasks },
		{ id: "health", label: t("suites.tabs.health") },
		{ id: "tiers", label: t("suites.tabs.tiers"), badge: suite.tiers?.length || undefined },
		{ id: "calibration", label: t("suites.tabs.calibration"), hidden: !judged },
		{ id: "export", label: t("suites.tabs.export") },
		{ id: "ops", label: t("tab", { ns: "suiteops" }) },
	];

	return (
		<section className="flex min-w-0 flex-col gap-6" data-testid="suite-screen">
			<PageHeader
				title={
					<span className="flex flex-wrap items-baseline gap-x-2">
						<span className="font-mono">{suite.id}</span>
						{suite.version && <span className="text-muted-foreground font-mono text-sm font-normal">{suite.version}</span>}
					</span>
				}
				breadcrumbs={crumbs}
				subtitle={suite.description ? <span className="line-clamp-2">{suite.description}</span> : undefined}
				meta={
					<>
						<ModeBadge mode={mode as never} />
						{!suite.error && <TrackBadge track={suiteTrack(suite)} />}
						{judged && <GradingBadge grading="judged" />}
						{!suite.error && <LockBadge state={suite.lock_state} testId="suite-lock" />}
					</>
				}
				actions={
					!suite.error && (
						<Button size="sm" variant="outline" asChild>
							<Link to="/leaderboard" search={{ suite: leaderboardSuite(suite) }} data-testid="suite-leaderboard-link">
								<Trophy className="size-4" aria-hidden="true" />
								{t("suites.facts.leaderboard")}
							</Link>
						</Button>
					)
				}
				testId="suite-header"
			/>
			{requestedVersion && requestedVersion !== suite.version && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="suite-version-mismatch"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.detail.versionMismatch", { requested: requestedVersion, current: suite.version || "?" })}
				</p>
			)}
			{mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-sm border px-4 py-2 text-sm"
					data-testid="suites-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.synthetic")}
				</p>
			)}
			{suite.error ? (
				<BrokenSuite suite={suite} />
			) : (
				<>
					<SuiteDriftNotice suite={suite} />
					<FingerprintDriftNotice suite={suite} />
					<details className="group min-w-0 rounded-sm border px-3 py-2" data-testid="suite-settings">
						<summary className="text-foreground flex cursor-pointer list-none items-center gap-1.5 text-sm font-medium">
							<ChevronRight className="size-4 transition-transform group-open:rotate-90" aria-hidden="true" />
							{t("suites.detail.settings")}
							<span className="text-muted-foreground text-xs font-normal">{t("suites.detail.settingsHint")}</span>
						</summary>
						<div className="pt-3">
							<SuiteFacts suite={suite} showLeaderboard={false} showImported={false} />
						</div>
					</details>
					<RouteTabs tabs={tabs} defaultTab="tasks" label={t("suites.tabs.label")} testId="suite-tabs">
						{(active) =>
							active === "health" ? (
								<SuiteHealthTab suite={suite} health={health} />
							) : active === "tiers" ? (
								<SuiteTiersTab suite={suite} />
							) : active === "calibration" ? (
								<SuiteCalibrationTab suite={suite} />
							) : active === "ops" ? (
								<OpsTab suite={suite} />
							) : active === "export" ? (
								<SuiteExportTab suite={suite} />
							) : (
								<SuiteTasksTab suite={suite} health={health} />
							)
						}
					</RouteTabs>
				</>
			)}
		</section>
	);
}