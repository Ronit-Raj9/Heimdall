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
import { ExperimentExportMenu, useExperimentExportItems } from "@/components/export/exportMenu"; // W10 export
import { ExperimentRunActions, RegradeAction } from "@/components/platform/experimentActions";
import { RunControlsPanel } from "@/components/experiments/runControls"; // run control
import { RunList } from "@/components/runs/runList";
import { BlockSkeleton, InlineError, LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { EntityLink } from "@/components/shell/entityLink";
import { DropdownMenu } from "@/components/ui/dropdownMenu";
import type { OverflowMenuItem } from "@/components/agents/overflowMenu";
import { useConfirm, type DestructiveConfirmOptions } from "@/components/platform/confirmDialog";
import { notify } from "@/lib/notify";
import { canUse, useSession } from "@/lib/session/sessionContext";
import "@/components/verdict/uf4aI18n";
import { activeNavItem } from "@/lib/constants/navigation";
import { usePageTitle } from "@/lib/routing/pageTitle";
import { ShareButton } from "@/components/share/shareDialog";
import { PageHeader } from "@/components/shell/pageHeader";
import { useArchiveItems } from "@/components/ui/archiveAction";
import { RouteTabs, type RouteTab } from "@/components/shell/routeTabs";
import { StatusBadge } from "@/components/shell/statusBadge";
import { Button } from "@/components/ui/button";
import { useGetExperimentAdaptiveQuery } from "@/lib/store/apis/adaptiveApi";
import { useAppDispatch } from "@/lib/store";
import { useGetCostExplainQuery } from "@/lib/store/apis/migrationApi";
import { skipToken } from "@reduxjs/toolkit/query/react";
import { comparisonSummaryMarkdown } from "@/components/verdict/comparisonSummary";
import { formatUsd2, observedNumber } from "@/components/verdict/armCost";
import { armOrder } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import "@/components/experiments/ufrcI18n";
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
import { useDeleteExperimentMutation } from "@/lib/store/apis/dataOpsApi";
import { Bot, ChevronDown, CornerUpLeft, Hourglass, Layers, ListChecks, SearchX } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ActionsTab } from "./actionsTab";
import { ANALYSIS_TAB_ID, AnalysisTab, AnalysisTabLabel } from "./analysisTab"; // L15 analysis jobs
import { CostTab } from "./costTab";
import {
	EXPERIMENT_TABS,
	behindMore,
	defaultExperimentTab,
	hiddenExperimentTabs,
	redirectExperimentTab,
	type ExperimentTabId,
} from "./experimentTabs";
import { RegradeNotice, gradeQuery, useGradeChoice } from "./gradeChoice";
import { IntegrityTab } from "./integrityTab";
import { LiveProgress } from "./liveProgress";
import { MatrixTab } from "./matrixTab";
import { MigrationTab } from "./migrationTab";
import { ReceiptsTab } from "./receiptsTab";
import { ScorecardsTab } from "./scorecardsTab";
import { ExperimentGradePicker, VerdictTab } from "./verdictTab";
import { GatewayEffectSlot } from "./gatewayEffectSlot";
import "./fw11I18n";
import "./hw12I18n";
import { FlipBanner } from "@/components/validity/flipBanner";

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
	// The newest re-score by default, ?grade=original for the original (gradeChoice.tsx).
	const { grade } = useGradeChoice(expId);
	const { t: tPage } = useTranslation("experimentPage");
	const { t: tx } = useTranslation("experimentsUx");
	const { t: tf } = useTranslation("fw11");
	const { t: th } = useTranslation("hw12");
	const [tabParam, setTab] = useQueryState("tab", parseAsString);
	// M4-15: an old ?tab=adaptive or ?tab=export link lands where that content lives now.
	const redirect = redirectExperimentTab(tabParam);
	const { t: tu } = useTranslation("uf4a");
	const { t: tr } = useTranslation("ufrc");
	const session = useSession();
	const canMember = canUse(session, "member");
	const [regradeOpen, setRegradeOpen] = useState(false);
	const [deleteError, setDeleteError] = useState<unknown>(null);
	const [copyError, setCopyError] = useState<unknown>(null);
	const exportItems = useExperimentExportItems(expId);
	useEffect(() => {
		if (redirect) void setTab(redirect === "verdict" ? null : redirect);
	}, [redirect, setTab]);
	const status = useGetExperimentQuery(expId);
	const live = status.data ? !TERMINAL_STATES.has(status.data.state) && status.data.state !== "created" : false;
	// Poll while it runs, so the header, the progress card and the runs tab stay current.
	useGetExperimentQuery(expId, {
		pollingInterval: live ? EXPERIMENT_POLL_MS : 0,
		skipPollingIfUnfocused: true,
		skip: !live,
	});
	// An experiment that has not started has no runs to analyse, so nothing asks for its verdict.
	const notStarted = status.data?.state === "created";
	const verdict = useGetVerdictQuery(gradeQuery(expId, grade), {
		skip: notStarted,
	});
	// The grid fills in while the experiment runs, so it polls alongside the status. It needs only the id,
	// so it starts with the status call, not after it (M4-6).
	const grid = useGetGridQuery(expId, {
		pollingInterval: live ? EXPERIMENT_POLL_MS : 0,
		skipPollingIfUnfocused: true,
	});
	// When a running experiment stops, its verdict, diagnostics, grid and runs were computed from a
	// partial experiment: refresh them once.
	const dispatch = useAppDispatch();
	const wasLive = useRef(live);
	useEffect(() => {
		if (wasLive.current && !live) dispatch(benchApi.util.invalidateTags(experimentResultTags(expId)));
		wasLive.current = live;
	}, [live, expId, dispatch]);
	// The facts that decide the tabs. Each is the request its tab makes anyway, so nothing is fetched twice.
	// They need only the id, so they start in parallel with the status call (M4-6).
	const adaptive = useGetExperimentAdaptiveQuery(expId);
	// Q-70: the pair's average cost for the summary; the same request the Result and Cost tabs make (shared cache).
	const costExplain = useGetCostExplainQuery((status.data?.arms?.length ?? 0) === 2 ? { expId } : skipToken);
	const support = useGetExperimentSupportQuery(expId);
	const research = useGetExperimentResearchQuery(expId);
	const routing = useGetExperimentRoutingQuery(expId);
	// Fix wave 3 header facts: the evaluation that made it (N203), the comparison it extends (N246), the run of a
	// debug experiment (N247) and the drift monitors that still exist (N257).
	const current = status.data && status.data.exp_id === expId ? (status.data as StatusFacts) : undefined;
	const parentId = current?.extends ?? "";
	const parent = useGetExperimentQuery(parentId, { skip: !parentId });
	const evaluationOf = useGetEvaluationQuery(current?.evaluation_id ?? "", { skip: !current?.evaluation_id });
	const kind: ExperimentKind | null = current ? experimentKind(current) : null;
	const debugRuns = useListRunsQuery({ exp: expId }, { skip: kind !== "debug" });
	const monitors = useDriftMonitorNames();
	// The browser tab names the comparison and its section (M1-8, M3-19); set once the title is known below.
	const titleForTab = status.data && status.data.exp_id === expId ? status.data.name?.trim() || expId : undefined;
	// shell.nav.* lives in the bench namespace, not platform (Q-4).
	const section = t(experimentSectionKey(current?.evaluation_id) as never);
	usePageTitle(titleForTab ? `${titleForTab} · ${String(section)} · Heimdall` : undefined);
	const deleteItem = useDeleteExperimentItem(expId, titleForTab ?? expId, live, setDeleteError);
	// Phase D decision 6: Archive sits above Delete in the More menu.
	const archive = useArchiveItems({
		kind: "experiment",
		id: expId,
		name: titleForTab ?? expId,
		archivedAt: (status.data as { archived_at?: string | null } | undefined)?.archived_at,
		testId: "experiment",
	});
	const copyId = async () => {
		setCopyError(null);
		try {
			await navigator.clipboard.writeText(expId);
			notify({ text: tu("copied", { ns: "toast" }) });
		} catch (e) {
			setCopyError(e);
		}
	};
	const headerError = exportItems.error ?? deleteError ?? copyError;
	const downloadSummary = () => {
		const data = verdict.data;
		const st = status.data;
		if (!data || !st) return;
		const arms = armOrder(data);
		const ce = costExplain.data;
		const costs: Record<string, string | undefined> = {};
		if (ce)
			for (const side of [ce.a, ce.b]) {
				const v = observedNumber(side.cost_per_run);
				if (v !== null) costs[side.arm] = formatUsd2(v, getLocale());
			}
		const names = Object.fromEntries((st.setups ?? []).map((x) => [x.arm, [x.agent, x.model].filter(Boolean).join(" on ")]));
		const text = comparisonSummaryMarkdown({
			title: st.name?.trim() || expId,
			expId,
			suite: st.suite,
			verdict: data.verdict,
			arms,
			naive: data.naive,
			names,
			costs,
			url: typeof window !== "undefined" ? window.location.href.split("?")[0] : undefined,
			date: new Date(),
		});
		const a = document.createElement("a");
		a.href = URL.createObjectURL(new Blob([text], { type: "text/markdown" }));
		a.download = `${expId}-summary.md`;
		a.click();
		URL.revokeObjectURL(a.href);
	};

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
	const allTabs: RouteTab[] = EXPERIMENT_TABS.map((id) => ({
		id,
		// One setup is scored on its own, so its result tab reads "Score", not a comparison.
		label: id === "actions" ? tActions("tab") : id === "verdict" && arms.length === 1 ? tPage("tabs.verdictScore") : tPage(`tabs.${id}`),
		hidden: hidden.has(id) && !shownOnRequest(id),
		testId: `experiment-tab-${id}`,
	}));
	allTabs.push({ id: ANALYSIS_TAB_ID, label: <AnalysisTabLabel />, testId: `experiment-tab-${ANALYSIS_TAB_ID}` });
	// M4-15: Result, Runs, Cost and Checks in the strip; the views that apply beside them sit behind More.
	const moreTabs = allTabs.filter((tab) => !tab.hidden && behindMore(tab.id));
	const tabs: RouteTab[] = allTabs.map((tab) => (!tab.hidden && behindMore(tab.id) ? { ...tab, hidden: true, addressable: true } : tab));
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
			<FlipBanner expId={expId} />
			<PageHeader
				breadcrumbs={
					// M1-7, M5-7: a benchmark of an evaluation sits under Evaluations; the first crumb is the sidebar's own label.
					evaluationId
						? [
								{
									label: String(t("shell.nav.evaluations" as never)),
									to: "/evaluations",
								},
								{
									label: evaluationName ?? th("header.evaluationCrumb", { name: evaluationId }),
									to: "/evaluations/$evalId",
									params: { evalId: evaluationId },
								},
								{ label: title },
							]
						: [
								{
									label: String(t(experimentSectionKey(undefined) as never)),
									to: "/experiments",
								},
								{ label: title },
							]
				}
				title={
					<span className="flex min-w-0 items-center gap-2.5">
						<span className="min-w-0 break-words" title={expId}>
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
						) : null}
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
						</span>
					</span>
				}
				actions={
					<div className="flex flex-wrap items-center gap-1.5" data-testid="experiment-actions">
						<ExperimentRunActions status={st} />
						<ShareButton kind="verdict" target={expId} />
						{/* Without its button the re-score action is only its dialog and log: kept out of the page column so it adds no empty gap. */}
						{canMember && <RegradeAction expId={expId} open={regradeOpen} onOpenChange={setRegradeOpen} hideButton />}
					</div>
				}
				// M4-14: Re-score, Compare runs, the three downloads, Copy id and, after a line, Delete in the shared More menu.
				moreLabel={tPage("header.more")}
				more={[
					...(canMember ? [{ label: tu("header.rescore"), onSelect: () => setRegradeOpen(true), testId: "experiment-regrade" }] : []),
					{
						label: tu("header.compare"),
						to: "/compare",
						search: compareSearch(undefined, expId) as Record<string, unknown>,
						testId: "experiment-compare",
					},
					// Round 3: the file name made each download line 600 px wide; the menu names the kind of file only.
					...exportItems.items.map((item) => ({ ...item, label: menuLabel(item.label) })),
					// Q-70: a summary a person can read and send on, built from what the page has.
					...(verdict.data && !kind ? [{ label: tr("report.menu"), onSelect: () => downloadSummary(), testId: "experiment-summary" }] : []),
					{ label: tu("header.copyId", { id: "" }).trim(), onSelect: () => void copyId(), separatorBefore: true, testId: "experiment-copy-id" },
					...archive.items.map((item) => ({ ...item, separatorBefore: true })),
					deleteItem,
				]}
			/>

			<InlineError error={headerError} testId="experiment-header-error" />
			{archive.alert}

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

			{!notStarted && kind !== "debug" && <RegradeNotice expId={expId} />}

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

			{!notStarted && <RunControlsPanel expId={expId} state={st.state} grades={<ExperimentGradePicker expId={expId} />} />}

			<div className="min-w-0">
				<RouteTabs
					// M4-14, M4-15: the More views menu sits in the strip's trailing slot, outside its fade.
					trailing={
						moreTabs.length > 0 ? (
							<div className="flex h-9 items-center pl-1" data-testid="experiment-more-views">
								<DropdownMenu
									label={tu("header.moreViewsLabel")}
									align="end"
									testId="experiment-more-views-menu"
									items={moreTabs.map((tab) => ({
										id: tab.id,
										label: tab.label,
										to: "/experiments/$expId",
										params: { expId },
										search: { tab: tab.id },
										testId: `experiment-more-${tab.id}`,
									}))}
									trigger={(p) => (
										<Button {...p} variant="ghost" size="sm" className="gap-1" dataTestId="experiment-more-views-button">
											{tu("header.moreViews")}
											<ChevronDown aria-hidden="true" />
										</Button>
									)}
								/>
							</div>
						) : undefined
					}
					tabs={tabs}
					defaultTab={defaultExperimentTab(arms.length)}
					label={t("experiments.page.tabsLabel")}
					testId="experiment-tabs"
				>
					{(active) =>
						active === ANALYSIS_TAB_ID ? (
							<AnalysisTab expId={expId} gradeSource={gradeQuery(expId, grade).gradeSource} />
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
								live={live}
								gradeSource={gradeQuery(expId, grade).gradeSource}
							/>
						)
					}
				</RouteTabs>
			</div>
		</div>
	);
}

/** The bench key of the sidebar section a comparison sits under: Evaluations for a benchmark of an evaluation. Pure. */
export function experimentSectionKey(evaluationId: string | undefined): string {
	return evaluationId ? "shell.nav.evaluations" : (activeNavItem("/experiments")?.labelKey ?? "shell.nav.experiments");
}

/** Whether the More menu's Delete is off and why: admins only, and never while the comparison runs. Pure. */
export function deleteBlocked(
	admin: boolean,
	running: boolean,
	texts: { adminOnly: string; busy: string },
): { disabled: boolean; disabledReason?: string } {
	if (!admin) return { disabled: true, disabledReason: texts.adminOnly };
	if (running) return { disabled: true, disabledReason: texts.busy };
	return { disabled: false };
}

/**
 * Delete (admin) as an item of the More menu: the shared confirm names what goes, then delete, a
 * notice and back to the list. Disabled with the reason while the comparison runs or below admin.
 */
function useDeleteExperimentItem(expId: string, name: string, running: boolean, onError: (e: unknown) => void): OverflowMenuItem {
	const { t } = useTranslation("experimentPage");
	const { t: tu } = useTranslation("uf4a");
	const session = useSession();
	const navigate = useNavigate();
	const confirm = useConfirm();
	const [remove] = useDeleteExperimentMutation();
	const admin = canUse(session, "admin");
	const opts: DestructiveConfirmOptions = {
		destructive: true,
		title: tu("header.deleteTitle"),
		confirmLabel: tu("header.deleteOk"),
		cancelLabel: t("header.deleteKeep"),
	};
	return {
		label: tu("header.delete"),
		destructive: true,
		separatorBefore: true,
		...deleteBlocked(admin, running, { adminOnly: t("header.deleteAdminOnly"), busy: t("header.deleteBusy") }),
		testId: "experiment-delete",
		onSelect: () =>
			void (async () => {
				if (!(await confirm(t("header.deleteConfirm"), opts))) return;
				onError(null);
				try {
					await remove(expId).unwrap();
					notify({ text: t("deleted", { ns: "toast", name }) });
					void navigate({ to: "/experiments" });
				} catch (e) {
					onError(e);
				}
			})(),
	};
}

function ExperimentTab({
	tab,
	expId,
	arms,
	suite,
	suiteVersion,
	evaluationId,
	live,
	gradeSource,
}: {
	tab: ExperimentTabId;
	expId: string;
	arms: readonly string[];
	suite?: string;
	suiteVersion?: string;
	evaluationId?: string;
	live?: boolean;
	/** The grade source the page shows (gradeQuery): the tabs that read the verdict read that one (decision 1). */
	gradeSource?: string;
}) {
	switch (tab) {
		case "verdict":
			return (
				<>
					<GatewayEffectSlot expId={expId} />
					<VerdictTab expId={expId} evaluationId={evaluationId} />
				</>
			);
		case "runs":
			return <RunList expId={expId} running={live} testId="experiment-runs" />;
		case "matrix":
			return <MatrixTab expId={expId} gradeSource={gradeSource} />;
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
			return <ActionsTab expId={expId} suite={suite} suiteVersion={suiteVersion} gradeSource={gradeSource} />;
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
/** A download line without its file name ("Download spreadsheet (CSV): x.csv, 10 runs" to "Download spreadsheet (CSV), 10 runs"). Pure. */
export function menuLabel(label: string): string {
	return label.replace(/: [^\s,]+(?=,|$)/, "");
}
