import { BuiltinReadOnlyNote } from "@/components/suites/builtinReadOnly";
import { ReferenceBoardPanel } from "@/components/reference/referenceBoard"; // G4-03: how known agents score on a library benchmark
import "@/lib/types/referenceI18n";
import { BlockSkeleton, LoadError, Section } from "@/components/runs/screenStates";
import { GradingBadge } from "@/components/scenario/scenarioBadges";
import { suiteGrading } from "@/components/scenario/scenario.utils";
import { EmptyState, PageHeader, RouteTabs, suiteIdOf, type RouteTab } from "@/components/shell";
import { suiteVersionOf } from "@/components/shell/entityLink";
import { isBuiltinSuite, suiteDisplayDescription } from "@/components/suites/suiteDisplay";
import { SuiteStatusPill, suiteDisplayName } from "@/components/suites/suiteIdentity";
import { SuiteRunButton } from "@/components/suites/suiteRunButton";
import { BrokenSuite, SuiteDriftNotice, SuiteFacts } from "@/components/suites/suiteFacts";
import { FingerprintDriftNotice } from "@/components/suites/suiteLockKind";
import { healthState } from "@/components/suites/suiteHealth.utils";
import { leaderboardSuite } from "@/components/suites/suites.utils";
import { Button } from "@/components/ui/button";
import { getBenchErrorMessage, useGetSuiteHealthQuery, useGetSuitesQuery, useListRunsQuery } from "@/lib/store";
import "@/app/failure-tests/views/gw07I18n";
import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, CircleCheck, History, ListChecks, type LucideIcon, Radar, Trophy } from "lucide-react";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { BuildTab } from "./buildTab";
import { PolicyTab } from "./policyTab";
import { SuiteCalibrationTab } from "./calibrationTab";
import { DataTab } from "./dataTab";
import { OpsTab } from "./opsTab";
import { EditTab } from "./editTab";
import { SuiteExportTab, SuiteHealthTab, SuiteTiersTab } from "./suiteTabs";
import { SuiteDeleteSection } from "./suiteDeleteSection";
import { SuiteTasksTab } from "./tasksTab";
import { ReviewTab } from "./reviewTab";

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
	const crumbs = [{ label: t("suites.title"), to: "/suites" }, { label: suiteDisplayName(suiteIdOf(suiteId), suite ?? undefined) }];

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
	const { t: tu } = useTranslation("suitesUx");
	const { t: tb } = useTranslation("suiteBuild");
	const { t: td } = useTranslation("benchData");
	const { t: tpd } = useTranslation("policyDocs");
	const { t: ta } = useTranslation("authoring");
	// Health is a separate request: a failure or a 503 (no run store) shows "health unavailable" and never blocks the page.
	const healthQuery = useGetSuiteHealthQuery(suite.id, {
		skip: Boolean(suite.error),
	});
	const health = healthState(healthQuery);
	const judged = suiteGrading(suite) === "judged";
	const builtin = isBuiltinSuite(suite);
	const tasks = suite.tasks.length;
	const definedTiers = (suite.tiers ?? []).filter((tier) => tier.defined).length;
	const tabs: RouteTab[] = [
		{ id: "tasks", label: tu("tabs.tasks"), badge: tasks },
		{ id: "build", label: tb("tab"), hidden: builtin }, // X13: grader, add tasks, held out, readiness
		{ id: "data", label: td("tab"), hidden: builtin }, // Y15: personal data policy, access, versions, platform imports
		{ id: "policy", label: tpd("tab"), hidden: builtin }, // Z23: policy document to compliance benchmark
		{ id: "review", label: ta("tab"), hidden: builtin }, // Z19: draft with a model, review generated tasks, quality checks, held out
		{ id: "settings", label: tu("tabs.settings") },
		{ id: "health", label: tu("tabs.health") },
		// C2.levels: the implicit nightly level (defined false) is not a difficulty level the suite set.
		{
			id: "tiers",
			label: tu("tabs.tiers"),
			hidden: definedTiers === 0,
			badge: definedTiers || undefined,
		},
		{ id: "calibration", label: tu("tabs.calibration"), hidden: !judged },
		{ id: "ops", label: tu("tabs.ops") },
		{ id: "edit", label: tu("tabs.edit"), hidden: builtin, aliases: ["files"] }, // L10 suite authoring; a built-in library suite is read-only; N271: ?tab=files opens it
		{ id: "export", label: tu("tabs.export") },
	];
	const { t: tg } = useTranslation("gw07");
	// Runs so far comes from the run list (GET /runs total), not the health report: the health report can be slow or
	// fail on a big benchmark, and "None yet" must only mean there are no runs (fix wave 2, N164).
	const runList = useListRunsQuery({ benchmark: suite.id, limit: 1 }, { skip: Boolean(suite.error) });
	const runsValue = runList.data ? (
		runList.data.total === 0 ? (
			tu("stats.never")
		) : (
			runList.data.total
		)
	) : runList.isError ? (
		<span title={getBenchErrorMessage(runList.error)} data-testid="suite-runs-error">
			-
		</span>
	) : (
		<span className="text-muted-foreground text-base font-normal">{tg("suite.counting")}</span>
	);

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="suite-screen">
			<PageHeader
				title={
					<span className="flex min-w-0 flex-col gap-0.5">
						<span className="truncate text-2xl font-semibold tracking-tight sm:text-3xl">{suiteDisplayName(suite.id, suite)}</span>
						<span className="text-muted-foreground truncate font-mono text-xs font-normal">
							{suite.id}
							{suite.version && <span className="opacity-70">@{suite.version}</span>}
						</span>
					</span>
				}
				breadcrumbs={crumbs}
				subtitle={
					<span className="flex flex-col gap-0.5" data-testid="suite-plain-subtitle">
						<span>
							{tu("pageSubtitle", {
								count: suite.task_count,
								skill: suiteSkill(suite) ?? tu("skillFallback"),
							})}
						</span>
						{suiteDisplayDescription(suite) && (
							<span className="text-muted-foreground line-clamp-2 text-xs">{suiteDisplayDescription(suite)}</span>
						)}
					</span>
				}
				meta={
					<>
						<SuiteStatusPill suite={suite} testId="suite-status" />
						{judged && <GradingBadge grading="judged" />}
					</>
				}
				actions={
					!suite.error && (
						<div className="flex flex-wrap items-center gap-2">
							<Button size="sm" variant="ghost" asChild>
								<Link to="/leaderboard" search={{ suite: leaderboardSuite(suite) }} data-testid="suite-leaderboard-link">
									<Trophy className="size-4" aria-hidden="true" />
									{t("suites.facts.leaderboard")}
								</Link>
							</Button>
							<SuiteRunButton suiteId={suite.id} size="lg" testId="suite-run" />
						</div>
					)
				}
				testId="suite-header"
			/>
			{!suite.error && (
				<div className="grid min-w-0 grid-cols-2 gap-4 lg:grid-cols-4" data-testid="suite-stats">
					<StatTile icon={ListChecks} label={tu("stats.tasks")} hint={tu("stats.tasksHint")} value={suite.task_count} />
					<StatTile
						icon={CircleCheck}
						label={tu("stats.admitted")}
						hint={tu("stats.admittedHint")}
						value={tu("stats.admittedValue", {
							ready: suite.admitted_count,
							total: suite.task_count,
						})}
						tone={suite.task_count > 0 && suite.admitted_count === suite.task_count ? "good" : "warn"}
					/>
					<StatTile
						icon={Radar}
						label={tu("stats.probes")}
						hint={tu("stats.probesHint")}
						value={suite.probes.length}
						note={tg("suite.probesHint")}
					/>
					<StatTile icon={History} label={tu("stats.lastRun")} hint={tu("stats.lastRunHint")} value={runsValue} />
				</div>
			)}
			{requestedVersion && requestedVersion !== suite.version && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
					data-testid="suite-version-mismatch"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.detail.versionMismatch", {
						requested: requestedVersion,
						current: suite.version || "?",
					})}
				</p>
			)}
			{mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
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
					{builtin && <BuiltinReadOnlyNote makeOwn />}
					{builtin && <SuiteReferenceScores suiteId={suite.id} />}
					<SuiteDriftNotice suite={suite} />
					<FingerprintDriftNotice suite={suite} />
					{health.state === "loading" && (
						<p className="text-muted-foreground text-sm" role="status" data-testid="suite-health-loading">
							{tg("suite.healthLoading")}
						</p>
					)}
					<RouteTabs tabs={tabs} defaultTab="tasks" label={t("suites.tabs.label")} testId="suite-tabs">
						{(active) =>
							active === "settings" ? (
								<div className="bg-card min-w-0 rounded-xl border p-5" data-testid="suite-settings">
									<SuiteFacts suite={suite} showLeaderboard={false} showImported={false} />
									<SuiteDeleteSection suiteId={suite.id} builtin={builtin} />
								</div>
							) : active === "health" ? (
								<SuiteHealthTab suite={suite} health={health} />
							) : active === "tiers" ? (
								<SuiteTiersTab suite={suite} />
							) : active === "calibration" ? (
								<SuiteCalibrationTab suite={suite} />
							) : active === "build" ? (
								<BuildTab suite={suite} />
							) : active === "data" ? (
								<DataTab suite={suite} />
							) : active === "policy" ? (
								<PolicyTab suite={suite} />
							) : active === "review" ? (
								<ReviewTab suite={suite} />
							) : active === "ops" ? (
								<OpsTab suite={suite} />
							) : active === "edit" ? (
								<EditTab suite={suite} />
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
/** One headline number of the suite (local StatCard until the shared one lands). */
function StatTile({
	icon: Icon,
	label,
	value,
	tone,
	hint,
	note,
}: {
	icon: LucideIcon;
	label: string;
	value: React.ReactNode;
	tone?: "good" | "warn";
	/** Plain explanation shown on hover. */
	hint?: string;
	/** A short line under the value, always shown. */
	note?: string;
}) {
	return (
		<div className="bg-card flex min-w-0 flex-col gap-2 rounded-xl border p-5" title={hint}>
			<span className={cn("text-muted-foreground flex items-center gap-1.5 text-sm", hint && "cursor-help")}>
				<Icon className="size-4" aria-hidden="true" />
				{label}
			</span>
			<span
				className={cn(
					"text-2xl font-semibold tracking-tight tabular-nums",
					tone === "good" && "text-chart-success-ink",
					tone === "warn" && "text-chart-warning-ink",
				)}
			>
				{value}
			</span>
			{note && <span className="text-muted-foreground text-xs">{note}</span>}
		</div>
	);
}

/** The skills a suite tests, in plain words for the subtitle: its most common task capabilities. */
function suiteSkill(suite: SuiteSummary): string | null {
	const counts = new Map<string, number>();
	for (const task of suite.tasks) for (const c of task.capabilities ?? []) counts.set(c, (counts.get(c) ?? 0) + 1);
	const top = [...counts.entries()]
		.sort((a, b) => b[1] - a[1])
		.slice(0, 2)
		.map(([c]) => c.replace(/[-_]/g, " "));
	return top.length ? top.join(" and ") : null;
}

/** A built-in benchmark's reference scores: how known agents score here, or a plain note when none is published. */
function SuiteReferenceScores({ suiteId }: { suiteId: string }) {
	const { t } = useTranslation("reference");
	return (
		<Section
			title={t("benchmark.title")}
			description={t("benchmark.description")}
			testId="suite-reference"
			actions={
				<Link
					to="/leaderboard"
					search={{ tab: "reference" }}
					className="text-primary text-sm hover:underline"
					data-testid="suite-reference-all"
				>
					{t("benchmark.all")}
				</Link>
			}
		>
			<ReferenceBoardPanel benchmark={suiteId} />
		</Section>
	);
}