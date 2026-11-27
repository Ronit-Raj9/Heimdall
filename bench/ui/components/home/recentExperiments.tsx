import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { StatusBadge, suiteIdOf } from "@/components/shell";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage, useGetVerdictQuery } from "@/lib/store";
import "@/lib/types/homeI18n";
import "@/components/reportcard/reportCardI18n";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { TrackBadge } from "./homeBits";
import { humanExperimentName as experimentName } from "@/components/experiments/experiments.utils";
import type { ExperimentListItem } from "./home.utils";
import { isTestObject } from "@/lib/testObjects";

/** The newest experiments that are not running: suite icon, name, verdict and when it started. */
export function RecentExperiments({
	experiments,
	trackOf,
}: {
	experiments: readonly ExperimentListItem[];
	trackOf?: (suite: string | undefined) => string | undefined;
}) {
	const { t } = useTranslation("uf3");
	// Home hides the project's own test comparisons (M1-18); the comparisons list has the switch to show them.
	const shown = experiments.filter((exp) => !isTestObject(exp.exp_id) && !isTestObject(exp.name));
	return (
		<>
			<ul className="divide-border flex flex-col divide-y" data-testid="home-recent-list">
				{shown.map((exp) => (
					<RecentRow key={exp.exp_id} exp={exp} trackOf={trackOf} />
				))}
			</ul>
			{/* A path from Home to the runs that failed (M5-36). */}
			<Link
				to={"/runs" as never}
				search={{ outcome: "fail" } as never}
				className="text-primary block px-4 py-2 text-sm font-medium hover:underline sm:px-5"
				data-testid="home-recent-failures"
			>
				{t("home.recentFailures")}
			</Link>
		</>
	);
}

function RecentRow({ exp, trackOf }: { exp: ExperimentListItem; trackOf?: (suite: string | undefined) => string | undefined }) {
	const { t } = useTranslation("home");
	const { t: tx } = useTranslation("experimentsUx");
	const locale = getLocale();
	// The list may already carry the suite and decision; otherwise the verdict route has both.
	const needsVerdict = exp.state === "done" && Boolean(exp.analysis_id) && (exp.decision === undefined || exp.suite === undefined);
	const verdict = useGetVerdictQuery(exp.exp_id, { skip: !needsVerdict });
	const decision = exp.decision ?? verdict.data?.verdict.decision;
	const suite = exp.suite ?? verdict.data?.suite;
	const when = exp.started_at ?? exp.finished_at;

	return (
		<li data-testid={`home-recent-${exp.exp_id}`}>
			<Link
				to="/experiments/$expId"
				params={{ expId: exp.exp_id }}
				className="group hover:bg-muted focus-visible:outline-ring flex min-w-0 items-center gap-3 px-4 py-3 transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 sm:px-5"
				title={exp.exp_id}
			>
				<TrackBadge track={trackOf?.(suite)} />
				{/* Phones: the verdict sits under the name, so the name gets the row's width (Q-120). */}
				<span className="flex min-w-0 flex-1 flex-col gap-1 sm:flex-row sm:items-center sm:gap-3">
					<span className="flex min-w-0 flex-1 flex-col gap-0.5">
						{/* Phones: names and captions wrap to two lines instead of being cut (M2-15). */}
						<span className="text-foreground line-clamp-2 text-sm font-medium break-words sm:line-clamp-1">
							{/* The same name as the comparison page header: the saved title, never a title-cased id (M4-115). */}
							{experimentName(exp, (k, o) => String(tx(k as never, (o ?? {}) as never)))}
						</span>
						<span className="text-muted-foreground line-clamp-2 text-xs break-words sm:line-clamp-1" data-testid={`home-recent-suite-${exp.exp_id}`}>
							{suite ? suiteIdOf(suite) : "-"}
							<span className="sm:hidden">
								{" · "}
								{when ? formatRelative(when, locale) : t("recent.notStarted")}
							</span>
						</span>
					</span>
					<span className="shrink-0 self-start sm:self-auto" data-testid={`home-recent-verdict-${exp.exp_id}`}>
						{decision ? (
							<StatusBadge decision={decision} className="rounded-full border-0 px-2" />
						) : exp.state !== "done" ? (
							<StatusBadge state={exp.state} testId={`home-recent-state-${exp.exp_id}`} className="rounded-full border-0 px-2" />
						) : verdict.isLoading ? (
							<span className="bg-muted inline-block h-5 w-20 animate-pulse rounded-full" aria-hidden="true" />
						) : verdict.error ? (
							<span className="text-muted-foreground text-xs" title={getBenchErrorMessage(verdict.error)}>
								{t("recent.unavailable")}
							</span>
						) : (
							<span className="text-muted-foreground text-xs">{t("recent.pending")}</span>
						)}
					</span>
				</span>
				<span className="text-muted-foreground hidden w-24 shrink-0 text-right text-xs whitespace-nowrap tabular-nums sm:block">
					{when ? (
						<time dateTime={when} title={formatWhen(when, locale) ?? undefined}>
							{formatRelative(when, locale)}
						</time>
					) : (
						t("recent.notStarted")
					)}
				</span>
				<ChevronRight
					className="text-muted-foreground size-4 shrink-0 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none"
					aria-hidden="true"
				/>
			</Link>
		</li>
	);
}
