import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { StatusBadge, suiteIdOf } from "@/components/shell";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage, useGetVerdictQuery } from "@/lib/store";
import "@/lib/types/homeI18n";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { TrackBadge } from "./homeBits";
import { humanExperimentName, type ExperimentListItem } from "./home.utils";

/** The newest experiments that are not running: suite icon, name, verdict and when it started. */
export function RecentExperiments({
	experiments,
	trackOf,
}: {
	experiments: readonly ExperimentListItem[];
	trackOf?: (suite: string | undefined) => string | undefined;
}) {
	return (
		<ul className="divide-border/70 flex flex-col divide-y" data-testid="home-recent-list">
			{experiments.map((exp) => (
				<RecentRow key={exp.exp_id} exp={exp} trackOf={trackOf} />
			))}
		</ul>
	);
}

function RecentRow({ exp, trackOf }: { exp: ExperimentListItem; trackOf?: (suite: string | undefined) => string | undefined }) {
	const { t } = useTranslation("home");
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
				className="group hover:bg-accent/50 focus-visible:bg-accent/50 flex min-w-0 items-center gap-3 px-4 py-3 transition-colors focus-visible:outline-none sm:px-5"
				title={exp.exp_id}
			>
				<TrackBadge track={trackOf?.(suite)} />
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-foreground truncate text-sm font-medium">{humanExperimentName(exp, { check: t("running.checkName"), untitled: t("running.untitled") })}</span>
					<span className="text-muted-foreground truncate text-xs" data-testid={`home-recent-suite-${exp.exp_id}`}>
						{suite ? suiteIdOf(suite) : "-"}
						<span className="sm:hidden">
							{" · "}
							{when ? formatRelative(when, locale) : t("recent.notStarted")}
						</span>
					</span>
				</span>
				<span className="shrink-0" data-testid={`home-recent-verdict-${exp.exp_id}`}>
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
				<span className="text-muted-foreground hidden w-20 shrink-0 text-right text-xs tabular-nums sm:block">
					{when ? (
						<time dateTime={when} title={formatWhen(when, locale) ?? undefined}>
							{formatRelative(when, locale)}
						</time>
					) : (
						t("recent.notStarted")
					)}
				</span>
				<ChevronRight
					className="text-muted-foreground/60 size-4 shrink-0 transition-transform group-hover:translate-x-0.5 motion-reduce:transition-none"
					aria-hidden="true"
				/>
			</Link>
		</li>
	);
}
