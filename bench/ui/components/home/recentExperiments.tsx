import { formatRelative, formatWhen } from "@/components/platform/platform.utils";
import { EntityLink, StatusBadge, suiteIdOf } from "@/components/shell";
import { ValueCell } from "@/components/valueCell";
import { getLocale } from "@/lib/i18n";
import { getBenchErrorMessage, useGetVerdictQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";
import type { ExperimentListItem } from "./home.utils";

/**
 * The newest experiments that are not running: name and id, state, verdict decision, suite,
 * start time and spend. Rows wrap into two lines on a narrow screen instead of scrolling.
 */
export function RecentExperiments({ experiments }: { experiments: readonly ExperimentListItem[] }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-0 flex-col" data-testid="home-recent-list">
			<div
				className="text-muted-foreground hidden grid-cols-[minmax(0,2fr)_minmax(0,0.85fr)_minmax(0,1.15fr)_minmax(0,1fr)_minmax(0,0.85fr)_minmax(0,0.7fr)] gap-3 border-b px-3 pb-2 text-xs font-medium md:grid"
				aria-hidden="true"
			>
				<span>{t("home.recent.col.experiment")}</span>
				<span>{t("home.recent.col.state")}</span>
				<span>{t("home.recent.col.verdict")}</span>
				<span>{t("home.recent.col.suite")}</span>
				<span>{t("home.recent.col.started")}</span>
				<span className="text-right">{t("home.recent.col.cost")}</span>
			</div>
			<ul className="divide-border/70 flex flex-col divide-y">
				{experiments.map((exp) => (
					<RecentRow key={exp.exp_id} exp={exp} />
				))}
			</ul>
		</div>
	);
}

function RecentRow({ exp }: { exp: ExperimentListItem }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	// The list may already carry the suite and decision; otherwise the verdict route has both.
	const needsVerdict = exp.state === "done" && Boolean(exp.analysis_id) && (exp.decision === undefined || exp.suite === undefined);
	const verdict = useGetVerdictQuery(exp.exp_id, { skip: !needsVerdict });
	const decision = exp.decision ?? verdict.data?.verdict.decision;
	const suite = exp.suite ?? verdict.data?.suite;
	const startedAt = exp.started_at;

	return (
		<li
			className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1.5 px-3 py-2.5 md:grid-cols-[minmax(0,2fr)_minmax(0,0.85fr)_minmax(0,1.15fr)_minmax(0,1fr)_minmax(0,0.85fr)_minmax(0,0.7fr)]"
			data-testid={`home-recent-${exp.exp_id}`}
		>
			<div className="flex min-w-0 flex-col">
				<EntityLink kind="experiment" id={exp.exp_id} className="truncate text-sm font-medium no-underline">
					{exp.name || exp.exp_id}
				</EntityLink>
				{exp.name && (
					<span className="text-muted-foreground truncate font-mono text-[11px]" title={exp.exp_id}>
						{exp.exp_id}
					</span>
				)}
			</div>
			<div className="flex items-center justify-end gap-1.5 md:contents">
				<span className="md:flex md:items-center">
					<StatusBadge state={exp.state} testId={`home-recent-state-${exp.exp_id}`} />
				</span>
				<span className="md:flex md:items-center" data-testid={`home-recent-verdict-${exp.exp_id}`}>
					{decision ? (
						<StatusBadge decision={decision} />
					) : verdict.isLoading ? (
						<span className="bg-muted inline-block h-5 w-20 animate-pulse rounded-[5px]" aria-hidden="true" />
					) : verdict.error ? (
						<span className="text-muted-foreground text-xs" title={getBenchErrorMessage(verdict.error)}>
							{t("home.recent.verdictError")}
						</span>
					) : (
						<span className="text-muted-foreground hidden text-xs md:inline">{t("shell.status.noDecision")}</span>
					)}
				</span>
			</div>
			<div className="text-muted-foreground col-span-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs md:contents">
				<span className="min-w-0 truncate md:text-xs">
					{suite ? (
						<EntityLink kind="suite" id={suite} className="font-mono text-[0.92em]">
							{suiteIdOf(suite)}
						</EntityLink>
					) : (
						<span aria-hidden="true">-</span>
					)}
				</span>
				<span className="tabular-nums">
					{startedAt ? (
						<time dateTime={startedAt} title={formatWhen(startedAt, locale) ?? undefined}>
							{formatRelative(startedAt, locale)}
						</time>
					) : (
						t("home.recent.notStarted")
					)}
				</span>
				<span className="md:text-right">
					<ValueCell value={exp.spent_usd} kind="usd" className="text-foreground" />
				</span>
			</div>
		</li>
	);
}