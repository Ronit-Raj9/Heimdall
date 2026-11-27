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
import { JobRunButton } from "@/components/jobs/jobRunButton";
import { getBenchErrorMessage, useGetSuiteHealthQuery, useListRunsQuery } from "@/lib/store";
import { useGetSuiteQuery } from "@/lib/store/apis/suitesLightApi";
import "@/app/failure-tests/views/gw07I18n";
import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, ListChecks, Trophy } from "lucide-react";
import { useGetReferenceBoardQuery } from "@/lib/store/apis/referenceApi";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { READINESS_CHECKLIST_ID } from "@/components/suites/readinessChecklist";
import "@/components/suites/uf6aI18n";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect } from "react";
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
	// A suite reference may carry its version ("coding-core@1.0.0"), as verdicts and runs do.
	// N194: one benchmark from GET /suites/{id}, never the whole list; a 404 is a benchmark that is not listed.
	const query = useGetSuiteQuery(suiteIdOf(suiteId));
	const notListed = (query.error as { status?: unknown } | undefined)?.status === 404;
	// currentData, not data: after a link to another benchmark the old one is never shown under the new id.
	const suite = query.currentData ?? null;
	const crumbs = [{ label: t("suites.title"), to: "/suites" }, { label: suiteDisplayName(suiteIdOf(suiteId), suite ?? undefined) }];

	if (query.isLoading || (!suite && query.isFetching)) {
		return (
			<section className="flex min-w-0 flex-col gap-6" data-testid="suite-screen">
				<PageHeader title={suiteId} breadcrumbs={crumbs} />
				<BlockSkeleton rows={6} testId="suite-loading" />
			</section>
		);
	}
	if (query.isError && !notListed) {
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
	return <SuiteBody suite={suite} mode={suite.mode} crumbs={crumbs} requestedVersion={suiteVersionOf(suiteId)} />;
}

/**
 * M6-11: the five tabs of a benchmark and the old tab ids each one answers to, so old links keep
 * working (?tab=ops opens Build at the readiness section). Exported for tests.
 */
export const TAB_ALIASES = {
	build: ["ops", "policy", "review"],
	quality: ["health", "tiers", "calibration"],
	files: ["edit", "export"],
	settings: ["data"],
} as const;

/** The section id an old tab id scrolls to inside its new tab (ops opens Build at the readiness steps). */
export function aliasSection(tab: string | null): string | null {
	switch (tab) {
		case "ops":
			return "suite-section-readiness";
		case "policy":
		case "review":
		case "tiers":
		case "calibration":
		case "export":
		case "data":
			return `suite-section-${tab}`;
		default:
			return null;
	}
}

/** M6-10: the one facts line under the title, "3641 tasks, 36 ready, 103 runs so far". */
export function headerFacts(
	parts: { tasks: string; ready: string; probes?: string | null; runs?: string | null },
): string {
	return [parts.tasks, parts.ready, parts.probes, parts.runs].filter(Boolean).join(", ");
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
	const { t: tpd } = useTranslation("policyDocs");
	const { t: tf } = useTranslation("uf6a");
	// Health is a separate request: a failure or a 503 (no run store) shows "health unavailable" and never blocks the page.
	const healthQuery = useGetSuiteHealthQuery(suite.id, {
		skip: Boolean(suite.error),
	});
	const health = healthState(healthQuery);
	const judged = suiteGrading(suite) === "judged";
	const builtin = isBuiltinSuite(suite);
	const tasks = suite.tasks.length;
	const definedTiers = (suite.tiers ?? []).filter((tier) => tier.defined).length;
	// M6-17: the policy section only applies to a support benchmark (a simulated customer to copy).
	const hasSupportTrack = suite.track === "support";
	// M6-11: five tabs; the old ids are aliases, so every old link still lands on its content.
	const tabs: RouteTab[] = [
		{ id: "tasks", label: tu("tabs.tasks"), badge: tasks },
		{ id: "build", label: tb("tab"), hidden: builtin, aliases: TAB_ALIASES.build },
		{ id: "quality", label: tf("tabs.quality"), aliases: TAB_ALIASES.quality },
		{ id: "files", label: tf("tabs.files"), aliases: TAB_ALIASES.files },
		{ id: "settings", label: tu("tabs.settings"), aliases: TAB_ALIASES.settings },
	];
	// An old tab id in the link scrolls its section into view once the merged tab renders.
	const [rawTab] = useQueryState("tab", parseAsString);
	useEffect(() => {
		const id = aliasSection(rawTab);
		if (!id || typeof document === "undefined") return;
		const timer = window.setTimeout(() => document.getElementById(id)?.scrollIntoView({ block: "start" }), 50);
		return () => window.clearTimeout(timer);
	}, [rawTab]);
	// M1-12: a task link from a run names the version it used in ?rev=.
	const [rev] = useQueryState("rev", parseAsString);
	const askedVersion = requestedVersion ?? rev;
	// M1-8, M3-19: the browser tab names the benchmark.
	usePageTitle(`${suiteDisplayName(suite.id, suite)} · ${t("suites.title")} · Heimdall`);
	const { t: tg } = useTranslation("gw07");
	// Runs so far comes from the run list (GET /runs total), not the health report: the health report can be slow or
	// fail on a big benchmark, and "None yet" must only mean there are no runs (fix wave 2, N164).
	const runList = useListRunsQuery({ benchmark: suite.id, limit: 1 }, { skip: Boolean(suite.error) });
	const runsText = runList.data
		? runList.data.total === 0
			? tf("header.noRuns")
			: tf("header.runs", { count: runList.data.total })
		: runList.isError
			? null
			: tg("suite.counting");
	const facts = headerFacts({
		tasks: tf("header.tasks", { count: suite.task_count }),
		ready: tf("header.ready", { count: suite.admitted_count }),
		probes: (suite.probes ?? []).length > 0 ? tf("header.probes", { count: (suite.probes ?? []).length }) : null,
		runs: runsText,
	});
	// M6-13: an own benchmark with no ready task says what to do, and offers the steps instead of Run.
	const noneReady = !builtin && !suite.error && suite.task_count > 0 && suite.admitted_count === 0;

	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="suite-screen">
			<PageHeader
				title={
					// M6-16: the h1 holds the name only; the id and version line sits under it in the subtitle.
					<span className="line-clamp-2 min-w-0 text-2xl font-semibold tracking-tight sm:line-clamp-1 sm:text-3xl">
						{suiteDisplayName(suite.id, suite)}
					</span>
				}
				breadcrumbs={crumbs}
				subtitle={
					<span className="flex flex-col gap-0.5" data-testid="suite-plain-subtitle">
						{headerLine(suiteDisplayName(suite.id, suite), suite.id, suite.version) && (
							<span className="text-muted-foreground truncate font-mono text-xs font-normal" data-testid="suite-header-line">
								{headerLine(suiteDisplayName(suite.id, suite), suite.id, suite.version)}
							</span>
						)}
						<span>
							{noneReady
								? tf("header.notReady", { count: suite.task_count })
								: describedByOwnWords(suite) && suiteDisplayDescription(suite)
									? suiteDisplayDescription(suite)
									: tu("pageSubtitle", {
											count: suite.task_count,
											skill: suiteSkill(suite) ?? tu("skillFallback"),
										})}
						</span>
						{/* M6-15: the benchmark's own words only from sm up; on a phone one sentence is enough. */}
						{!noneReady && !describedByOwnWords(suite) && suiteDisplayDescription(suite) && (
							<span className="text-muted-foreground hidden text-xs sm:line-clamp-2">{suiteDisplayDescription(suite)}</span>
						)}
						{!suite.error && (
							<span className="text-muted-foreground flex items-center gap-1.5 text-xs tabular-nums" data-testid="suite-stats">
								<ListChecks className="size-3.5 shrink-0" aria-hidden="true" />
								{facts}
								{runList.isError && (
									<>
										,{" "}
										<span title={getBenchErrorMessage(runList.error)} data-testid="suite-runs-error">
											-
										</span>
									</>
								)}
							</span>
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
							{noneReady ? (
								<Button size="lg" asChild>
									<Link
										to="/suites/$suiteId"
										params={{ suiteId: suite.id }}
										search={{ tab: "tasks" } as never}
										hash={READINESS_CHECKLIST_ID}
										data-testid="suite-make-ready"
									>
										{tf("header.makeReady")}
									</Link>
								</Button>
							) : (
								<SuiteRunButton suiteId={suite.id} size="lg" testId="suite-run" />
							)}
						</div>
					)
				}
				testId="suite-header"
			/>
			{askedVersion && askedVersion !== suite.version && (
				<p
					role="note"
					className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
					data-testid="suite-version-mismatch"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.detail.versionMismatch", {
						requested: askedVersion,
						current: suite.version || "?",
					})}
				</p>
			)}
			{mode === "synthetic" && (
				<p
					role="note"
					className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
					data-testid="suites-synthetic-banner"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
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
								<div className="flex min-w-0 flex-col gap-6" data-testid="suite-settings">
									<div className="bg-card min-w-0 rounded-xl border p-5">
										<SuiteFacts suite={suite} showLeaderboard={false} showImported={false} />
									</div>
									{!builtin && (
										<div id="suite-section-data" className="scroll-mt-4">
											<DataTab suite={suite} />
										</div>
									)}
									<SuiteDeleteSection suiteId={suite.id} builtin={builtin} />
								</div>
							) : active === "quality" ? (
								<div className="flex min-w-0 flex-col gap-8" data-testid="suite-quality">
									<SuiteHealthTab suite={suite} health={health} />
									{definedTiers > 0 && (
										<div id="suite-section-tiers" className="scroll-mt-4">
											<SuiteTiersTab suite={suite} />
										</div>
									)}
									{judged && (
										<div id="suite-section-calibration" className="scroll-mt-4">
											<SuiteCalibrationTab suite={suite} />
										</div>
									)}
								</div>
							) : active === "build" ? (
								<div className="flex min-w-0 flex-col gap-8" data-testid="suite-build">
									<BuildTab suite={suite} />
									<div id="suite-section-readiness" className="scroll-mt-4">
										<OpsTab suite={suite} />
									</div>
									{hasSupportTrack && (
										<section id="suite-section-policy" className="flex scroll-mt-4 flex-col gap-3" aria-label={tpd("tab")}>
											<h2 className="text-foreground text-base font-semibold tracking-tight">{tf("sections.policy")}</h2>
											<PolicyTab suite={suite} />
										</section>
									)}
									<section id="suite-section-review" className="flex scroll-mt-4 flex-col gap-3" aria-labelledby="suite-section-review-title">
										<h2 id="suite-section-review-title" className="text-foreground text-base font-semibold tracking-tight">
											{tf("sections.review")}
										</h2>
										<ReviewTab suite={suite} />
									</section>
									{/* M3-14: the create action for cheat checks lives here, not on the report card. */}
									{!builtin && !suite.id.endsWith("-twins") && suite.task_count > 0 && (
										<section id="suite-section-cheat" className="flex scroll-mt-4 flex-col gap-3" aria-labelledby="suite-section-cheat-title" data-testid="suite-cheat-checks">
											<h2 id="suite-section-cheat-title" className="text-foreground text-base font-semibold tracking-tight">
												{tf("sections.cheat")}
											</h2>
											<p className="text-muted-foreground max-w-prose text-sm">{tf("cheat.hint")}</p>
											<div>
												<JobRunButton
													kind="twins.make"
													params={{ suite: suite.id, max: 50 }}
													label={tf("cheat.add")}
													confirm={tf("cheat.confirm")}
													variant="outline"
												/>
											</div>
										</section>
									)}
								</div>
							) : active === "files" ? (
								<div className="flex min-w-0 flex-col gap-8" data-testid="suite-files">
									<EditTab suite={suite} />
									<div id="suite-section-export" className="scroll-mt-4">
										<SuiteExportTab suite={suite} />
									</div>
								</div>
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
/**
 * The Edit files tab entry. A built-in benchmark hides it, but ?tab=edit still opens it, so the
 * read-only note of the Edit tab is reachable (p12-8).
 */
export function editTabEntry(label: string, builtin: boolean): RouteTab {
	return { id: "edit", label, hidden: builtin, addressable: builtin, aliases: ["files"] };
}

/** The small line under the title: "id@version", or only "@version" when the title already is the id (p10-7). */
export function headerLine(name: string, id: string, version: string | undefined): string {
	const at = version ? `@${version}` : "";
	return name === id ? at : `${id}${at}`;
}

/**
 * Voice and support benchmarks test a whole conversation, which task capabilities do not describe:
 * their subtitle is the benchmark's own description (p10-7).
 */
export function describedByOwnWords(suite: Pick<SuiteSummary, "track">): boolean {
	const track: string | undefined = suite.track;
	return track === "voice" || track === "support";
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
	// M6-10: no empty "How known agents score here" box; the section shows only when scores exist.
	const board = useGetReferenceBoardQuery({ benchmark: suiteId });
	if (board.data && board.data.entries.length === 0) return null;
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