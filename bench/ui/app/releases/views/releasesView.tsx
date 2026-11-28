import { GovDialog } from "@/components/govern/govDialog";
import { ReleaseActions } from "@/components/govern/releaseActions";
import { liveReleases } from "@/components/govern/releaseAgent";
import { versionName } from "@/components/releases/releaseEvidence";
import { ReleasePanel } from "@/components/releases/releasePanel";
import { LoadError } from "@/components/runs/screenStates";
import { EmptyState } from "@/components/shell/emptyState";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useListExperimentsQuery, useListReleasesQuery } from "@/lib/store";
import { useGetValidityQuery } from "@/lib/store/apis/validityApi";
import { Link } from "@tanstack/react-router";
import type { ReleaseRecord } from "@/lib/types/bench";
import "@/components/canaries/uf7bI18n";
import { Plus, Rocket } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { releaseLinkMissing } from "@/components/releases/releases.utils";
import "@/components/livescore/ufl9I18n";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { isTestObject, useShowTests, withoutTests } from "@/lib/testObjects";
import { releaseAgent } from "@/components/govern/releaseAgent";
import "@/components/releases/ufk3I18n";
import { ChainBadge, ReleaseCards } from "./releaseCards";

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/**
 * What is live now (M7-77, Q-16): the newest release of each agent, newest agent first, as
 * "codex v1 (released 21 Nov by ana)". Reads liveReleases, so releases recorded from a comparison
 * (agent_config_id only) count too. Exported for tests.
 */
export function liveNow(releases: readonly ReleaseRecord[], t: (k: string, o?: Record<string, unknown>) => string): string[] {
	return liveReleases(releases).map(({ release: r, agent: a }) => {
		const d = new Date(r.created_at);
		const date = Number.isNaN(d.getTime()) ? "" : `${d.getDate()} ${MONTHS[d.getMonth()]}`;
		return t("releases.liveOne", { name: versionName(a.agent, a.version), date, approver: r.approver || "-" });
	});
}

/** True once the comparisons list is in and empty: nothing can be recorded (X-110, Q-312). Exported for tests. */
export function noComparisonYet(q: { isSuccess: boolean; data?: { experiments?: readonly unknown[] } }): boolean {
	return q.isSuccess && (q.data?.experiments ?? []).length === 0;
}

/** True for a release of a test agent (zz-test names, the shared rule other lists use). Exported for tests. */
export function isTestRelease(r: ReleaseRecord): boolean {
	const a = releaseAgent(r);
	return isTestObject(a?.agent) || isTestObject(r.agent_config_id) || isTestObject(r.exp_id);
}

/** The releases screen: the hash-chained release log as a timeline, newest first; "New release" opens the record dialog. */
export default function ReleasesView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u14");
	const { t: l9 } = useTranslation("ufl9");
	const releases = useListReleasesQuery();
	// ?exp=<id>: "Promote this version" on a verdict opens the form with that comparison picked.
	const [expParam, setExpParam] = useQueryState("exp", parseAsString);
	const [open, setOpen] = useState(Boolean(expParam));
	const all = releases.data?.releases ?? [];
	// Releases of test agents stay in the chained log, hidden here unless asked for (K3).
	const [showTests, setShowTests] = useShowTests();
	const { shown: list, hidden: hiddenTests } = withoutTests(all, isTestRelease, showTests);
	const { t: k3 } = useTranslation("ufk3");
	const { t: m } = useTranslation("uf7b");
	const experiments = useListExperimentsQuery();
	// X-110: with no comparison at all, "Record a release" leads nowhere, so the empty state points to an evaluation.
	const noComparison = noComparisonYet(experiments);
	const live = liveNow(list, m as never);
	const flipped = (useGetValidityQuery().data?.entries ?? []).filter((e) => e.flipped).length;
	// M7-83: ?release=<id> (the copied link) scrolls to that record once the list is in.
	const [releaseParam] = useQueryState("release", parseAsString);
	useEffect(() => {
		if (!releaseParam || list.length === 0) return;
		document.getElementById(`release-${releaseParam}`)?.scrollIntoView({ block: "start" });
	}, [releaseParam, list.length]);

	return (
		<section className="flex w-full flex-col gap-8" data-testid="releases-screen">
			{/* Q-71: the h1 and subtitle are the page's, above the tabs; this tab keeps its action row. */}
			<div className="flex flex-wrap items-center justify-between gap-3">
				{releases.isSuccess ? (
					<p className="min-w-0 text-sm" data-testid="releases-live-now">
						<span className="font-medium">{m("releases.liveLabel")}</span> {live.length ? live.join("; ") : m("releases.liveNone")}
					</p>
				) : (
					<span />
				)}
				{/* Q-312: with no comparison there is nothing to record; say why and where to start. */}
				<div className="flex flex-col items-end gap-1">
					<Button onClick={() => setOpen(true)} disabled={noComparison} aria-describedby={noComparison ? "release-new-reason" : undefined} data-testid="release-new">
						<Plus className="size-4" aria-hidden="true" />
						{tu("releases.new")}
					</Button>
					{noComparison && (
						<p id="release-new-reason" className="text-muted-foreground max-w-xs text-right text-xs" data-testid="release-new-reason">
							{tu("releases.emptyNoComparison")}{" "}
							<Link to="/evaluate" className="text-primary hover:underline">
								{m("releases.evaluateAgent")}
							</Link>
						</p>
					)}
				</div>
			</div>
			<div className="grid grid-cols-[minmax(0,1fr)] gap-8 xl:grid-cols-[minmax(0,1fr)_20rem] xl:items-start" data-testid="releases-layout">
				<div className="flex min-w-0 flex-col gap-4">
					<div className="flex flex-wrap items-center justify-between gap-2">
						<h2 className="text-sm font-semibold">{tu("releases.timeline")}</h2>
						<div className="flex items-center gap-2">
							{hiddenTests > 0 && (
								<Button variant="ghost" size="sm" onClick={() => setShowTests(!showTests)} data-testid="releases-show-tests">
									{showTests ? k3("releases.hideTests") : k3("releases.showTests", { count: hiddenTests })}
								</Button>
							)}
							{releases.data && <ChainBadge ok={releases.data.chain_ok} />}
						</div>
					</div>
					{releases.isLoading ? (
						<div className="flex flex-col gap-4" data-testid="release-timeline-loading">
							<Skeleton className="h-28 w-full rounded-xl" />
							<Skeleton className="h-28 w-full rounded-xl" />
						</div>
					) : releases.isError ? (
						<LoadError
							title={t("releases.timeline.loadError")}
							error={releases.error}
							onRetry={releases.refetch}
							testId="release-timeline-error"
						/>
					) : list.length === 0 && noComparison ? (
						<EmptyState
							icon={Rocket}
							message={tu("releases.emptyNoComparison")}
							action={{ label: m("releases.evaluateAgent"), to: "/evaluate" }}
							testId="release-timeline-empty"
						/>
					) : list.length === 0 ? (
						<EmptyState
							icon={Rocket}
							message={tu("releases.empty")}
							action={{ label: tu("releases.new"), onClick: () => setOpen(true) }}
							testId="release-timeline-empty"
						/>
					) : (
						<>
							{releaseLinkMissing(list, releaseParam) && (
								<p className="text-muted-foreground text-sm" data-testid="release-link-missing">
									{l9("release.notFound")}
								</p>
							)}
							<ReleaseCards releases={list} chainOk={releases.data?.chain_ok ?? false} />
						</>
					)}
					<p className="text-muted-foreground text-xs" data-testid="releases-disclaimer">
						{t("releases.disclaimer")}
					</p>
					{/* Q-78: the validity ledger, reachable from where releases are judged. */}
					<Link to="/validity" className="text-primary w-fit text-sm hover:underline" data-testid="releases-validity-link">
						{flipped > 0 ? m("releases.validityLinkCount", { count: flipped }) : m("releases.validityLink")}
					</Link>
				</div>
				<aside className="flex min-w-0 flex-col gap-6 xl:sticky xl:top-0">
					<ReleaseActions releases={list} onDone={() => void releases.refetch()} />
				</aside>
			</div>
			<GovDialog
				open={open}
				onClose={() => {
					setOpen(false);
					void setExpParam(null);
				}}
				title={tu("releases.newTitle")}
				description={tu("releases.newHint")}
				closeLabel={tu("releases.close")}
				testId="release-dialog"
			>
				<div className="[&_[data-slot=card-header]]:hidden [&>[data-testid=release-panel]]:border-0 [&>[data-testid=release-panel]]:bg-transparent [&>[data-testid=release-panel]]:py-0 [&_[data-slot=card-content]]:px-0">
					<ReleasePanel initialExpId={expParam ?? undefined} />
				</div>
			</GovDialog>
		</section>
	);
}
