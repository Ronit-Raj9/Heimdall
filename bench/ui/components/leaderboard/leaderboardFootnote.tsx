import { formatDateTime } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { Leaderboard } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

export interface LeaderboardFootnoteProps {
	board: Pick<Leaderboard, "scope" | "condition" | "common_tasks" | "excluded" | "alpha" | "sort_metric" | "generated_at">;
	/** True when the board carries resilience columns. */
	hasResilience: boolean;
}

/** What the board rests on: the scope, the common tasks, and the runs left out by reason. */
export function LeaderboardFootnote({ board, hasResilience }: LeaderboardFootnoteProps) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const commonTasks = board.common_tasks ?? [];
	const excluded = board.excluded ?? [];
	const generated = formatDateTime(board.generated_at, locale) ?? board.generated_at;

	return (
		<footer className="text-muted-foreground flex flex-col gap-4 border-t pt-4 text-xs" data-testid="leaderboard-footnote">
			<h2 className="text-foreground text-sm font-semibold tracking-tight">{t("leaderboard.footnote.title")}</h2>
			<div className="grid gap-4 lg:grid-cols-3">
				<div className="flex flex-col gap-1.5" data-testid="leaderboard-footnote-scope">
					<p>
						{board.scope === "common"
							? t("leaderboard.footnote.scopeCommon", { count: commonTasks.length })
							: t("leaderboard.footnote.scopeAll")}
					</p>
					<p>{t("leaderboard.footnote.rankClaim")}</p>
					{hasResilience && <p>{t("leaderboard.footnote.resilience")}</p>}
					<p>{t("leaderboard.footnote.generated", { time: generated, metric: board.sort_metric, alpha: board.alpha })}</p>
				</div>

				<div className="flex flex-col gap-1.5" data-testid="leaderboard-footnote-common">
					<h3 className="text-foreground font-medium" title={t("leaderboard.footnote.commonTasksHint")}>
						{t("leaderboard.footnote.commonTasks", { count: commonTasks.length })}
					</h3>
					{commonTasks.length === 0 ? (
						<p>{t("leaderboard.footnote.noCommon")}</p>
					) : (
						<ul className="flex flex-wrap gap-1" aria-label={t("leaderboard.footnote.commonTasksHint")}>
							{commonTasks.map((task) => (
								<li key={task} className="bg-muted text-foreground rounded-sm px-1.5 py-0.5 font-mono text-[11px]">
									{task}
								</li>
							))}
						</ul>
					)}
				</div>

				<div className="flex flex-col gap-1.5" data-testid="leaderboard-footnote-excluded">
					<h3 className="text-foreground font-medium">{t("leaderboard.footnote.excluded")}</h3>
					{excluded.length === 0 ? (
						<p>{t("leaderboard.footnote.noExcluded")}</p>
					) : (
						<ul className="flex flex-col gap-1.5">
							{excluded.map((group) => (
								<li key={group.reason} className="flex flex-col gap-0.5" data-testid={`leaderboard-excluded-${group.reason}`}>
									<span>
										<span className="text-foreground font-mono">{group.reason}</span>
										{": "}
										<span className="tabular-nums">{t("leaderboard.footnote.excludedRuns", { count: group.runs })}</span>
									</span>
									{(group.experiments ?? []).length > 0 && (
										<span className="flex flex-wrap gap-x-2">
											{(group.experiments ?? []).map((id) => (
												<Link
													key={id}
													to="/verdict"
													search={{ exp: id }}
													aria-label={t("leaderboard.cell.openVerdict", { id })}
													className="text-primary font-mono text-[11px] underline-offset-4 hover:underline"
												>
													{id}
												</Link>
											))}
										</span>
									)}
								</li>
							))}
						</ul>
					)}
				</div>
			</div>
		</footer>
	);
}