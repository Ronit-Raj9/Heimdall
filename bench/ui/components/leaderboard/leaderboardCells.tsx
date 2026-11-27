import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EntityLink } from "@/components/shell";
import { CopyId } from "@/components/ui/copyId";
import { EmptyMark } from "@/components/ui/emptyMark";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ValueCell } from "@/components/valueCell";
import { formatValueNumber } from "@/components/valueCell.utils";
import { TONE_CLASS, TONE_TEXT, formatPValue, formatSignedPp, shortHash } from "@/components/verdict/verdict.utils";
import { getLocale } from "@/lib/i18n";
import type { LeaderboardEntry, Value } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import "@/components/verdict/hw09I18n";
import "@/components/matrix/ufc3I18n";
import { observedNumber, resilienceTone, successBar, tierOf } from "./leaderboard.utils";

// Cells of one leaderboard row. Every measured number goes through ValueCell or the same
// observed-or-not check, so an unknown is drawn hatched and never as 0.

/** Tier 1 "tied with leader" or tier 2 "behind", with the reason and the Holm-adjusted p on hover and focus. */
export function TierBadge({ entry }: { entry: Pick<LeaderboardEntry, "rank" | "tier" | "tier_reason" | "p_vs_leader"> }) {
	const { t } = useTranslation("bench");
	const tier = tierOf(entry);
	const hint =
		entry.p_vs_leader === null
			? t("leaderboard.tier.leaderHint")
			: t("leaderboard.tier.hint", { reason: entry.tier_reason, p: formatPValue(entry.p_vs_leader, getLocale()) });
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					tabIndex={0}
					aria-label={`${t(tier === 1 ? "leaderboard.tier.one" : "leaderboard.tier.two")}: ${hint}`}
					data-testid={`leaderboard-tier-${entry.rank}`}
					data-tier={tier}
					className={cn(
						"focus-visible:ring-ring/50 inline-flex cursor-help flex-col items-start rounded-sm border px-1.5 py-0.5 text-xs leading-tight focus-visible:ring-[3px]",
						tier === 1 ? TONE_CLASS.good : TONE_CLASS.neutral,
					)}
				>
					<span className="font-semibold">{t(tier === 1 ? "leaderboard.tier.one" : "leaderboard.tier.two")}</span>
					<span className="text-[11px] opacity-80">{t(tier === 1 ? "leaderboard.tier.oneShort" : "leaderboard.tier.twoShort")}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent className="max-w-xs">{hint}</TooltipContent>
		</Tooltip>
	);
}

/**
 * Agent config id, model, arm label, short config hash, and a synthetic badge when the runs were
 * scripted. showHash false leaves the hash out (the comparison matrix, M4-53); "What differs" is a
 * 32 px button either way.
 */
export function AgentCell({
	entry,
	showHash = true,
	compact = false,
}: {
	entry: Pick<LeaderboardEntry, "rank" | "agent" | "model" | "label" | "config_hash" | "synthetic">;
	showHash?: boolean;
	/** Two lines: name, then model and label; hash and "What differs" are left to the full table. */
	compact?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: tc } = useTranslation("ufc3");
	return (
		// M4-107: name, then model and version under it, narrow enough for a phone column.
		<div className="flex min-w-0 flex-col gap-0.5 sm:min-w-48" data-testid={`leaderboard-agent-${entry.rank}`}>
			<div className="flex items-center gap-1.5">
				<EntityLink kind="agent" id={entry.agent} className="font-sans font-medium" />
				{entry.synthetic && (
					<Badge
						variant="warning"
						className="px-1 py-0 text-[11px] tracking-wide uppercase"
						title={t("leaderboard.cell.syntheticHint")}
						data-testid={`leaderboard-synthetic-${entry.rank}`}
					>
						{t("leaderboard.cell.synthetic")}
					</Badge>
				)}
			</div>
			{compact ? (
				<span className="text-muted-foreground max-w-80 truncate text-xs" title={entry.label || entry.model}>
					<span className="font-mono">{entry.model}</span>
					{entry.label && entry.label !== entry.model && ` · ${entry.label}`}
				</span>
			) : (
				<span className="text-muted-foreground font-mono text-xs break-all">{entry.model}</span>
			)}
			{!compact && <div className="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
				{entry.label && <span className="max-w-56 truncate">{entry.label}</span>}
				{showHash && (
					<CopyId
						value={entry.config_hash}
						display={shortHash(entry.config_hash)}
						title={t("leaderboard.cell.configHash", { hash: entry.config_hash })}
						className="text-[11px]"
					/>
				)}
				<Button variant="outline" size="sm" className="h-8" asChild>
					<Link to="/agents" search={{ tab: "diff", b: entry.config_hash }} data-testid={`leaderboard-diff-${entry.rank}`}>
						{tc("matrix.whatDiffers")}
					</Link>
				</Button>
			</div>}
		</div>
	);
}

/**
 * Tasks solved under the task mean, on the same basis as the percent (fix wave 3, N295): null when the
 * entry does not say (an older server sends no tasks_solved; it is left out when 0, which only a
 * pass count of 0 makes certain).
 */
export function tasksSolved(entry: Partial<Pick<LeaderboardEntry, "tasks" | "tasks_solved" | "passes">>): number | null {
	if (entry.tasks === undefined) return null;
	if (entry.tasks_solved !== undefined) return entry.tasks_solved;
	return entry.passes?.num === 0 ? 0 : null;
}

/** Verified success as a bar with its 95% interval whiskers, the percent (mean over tasks), and the tasks solved. */
export function SuccessCell({
	entry,
}: {
	entry: Pick<LeaderboardEntry, "rank" | "success" | "passes"> & Partial<Pick<LeaderboardEntry, "tasks" | "tasks_solved">>;
}) {
	const { t } = useTranslation("bench");
	const { t: t9 } = useTranslation("hw09");
	const locale = getLocale();
	const bar = successBar(entry.success);
	const solved = tasksSolved(entry);
	const passes = (
		<span className="text-muted-foreground text-xs tabular-nums" title={t9("leaderboard.successHint")}>
			{solved !== null && entry.tasks !== undefined
				? t9("leaderboard.tasksSolved", { solved, tasks: entry.tasks })
				: t("leaderboard.cell.passes", { num: entry.passes.num, den: entry.passes.den })}
			{entry.passes.unknown > 0 && (
				<span className="bench-hatched ml-1 rounded-sm border border-dashed px-1">
					{t("leaderboard.cell.passesUnknown", { count: entry.passes.unknown })}
				</span>
			)}
		</span>
	);
	if (!bar) {
		return (
			<div className="flex flex-col items-start gap-1" data-testid={`leaderboard-success-${entry.rank}`}>
				<ValueCell value={entry.success} kind="rate" />
				{passes}
			</div>
		);
	}
	// The bar is clamped to 0..1; the words carry the server's numbers as they are.
	const text = formatValueNumber(observedNumber(entry.success) ?? bar.fill, "rate", locale);
	const bounds = entry.success.bounds;
	const label =
		bar.lo !== null && bar.hi !== null && bounds
			? t("leaderboard.cell.successAria", {
					value: text,
					lo: formatValueNumber(bounds[0], "rate", locale),
					hi: formatValueNumber(bounds[1], "rate", locale),
				})
			: t("leaderboard.cell.successAriaNoBounds", { value: text });
	return (
		<div className="flex min-w-36 flex-col gap-1" data-testid={`leaderboard-success-${entry.rank}`}>
			<div className="flex items-center gap-2">
				<div role="img" aria-label={label} title={label} className="bg-chart-track relative h-2.5 w-24 shrink-0 rounded-full">
					<div className="bg-chart-seq-3 absolute inset-y-0 left-0 rounded-full" style={{ width: `${bar.fill * 100}%` }} />
					{bar.lo !== null && bar.hi !== null && (
						<div
							className="border-foreground/70 absolute -inset-y-0.5 border-x-2"
							style={{ left: `${bar.lo * 100}%`, width: `${(bar.hi - bar.lo) * 100}%` }}
							data-testid={`leaderboard-interval-${entry.rank}`}
						>
							<div className="bg-foreground/70 absolute inset-x-0 top-1/2 h-px -translate-y-1/2" />
						</div>
					)}
				</div>
				<span className="text-foreground font-semibold tabular-nums">{text}</span>
			</div>
			{passes}
		</div>
	);
}

/**
 * pass^k with k = trials. Fewer than 2 tries per task measures no reliability: "Not measured", hatched,
 * with how many tries ran (fix wave 3, N245 and N295); "solved all k tries" only under a known value.
 */
export function ReliabilityCell({
	entry,
	inline = false,
}: {
	entry: Pick<LeaderboardEntry, "rank" | "reliability" | "trials">;
	/** A line under another cell: no per-cell "needs tries" note, the table says it once (M4-54). */
	inline?: boolean;
}) {
	const { t } = useTranslation("bench");
	const { t: t9 } = useTranslation("hw09");
	const known = observedNumber(entry.reliability) !== null;
	if (entry.trials < 2 && inline) {
		return (
			<span
				className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1 text-xs"
				data-state="unknown"
				data-testid={`leaderboard-reliability-${entry.rank}`}
				title={t9("reliability.needsTries", { k: entry.trials })}
			>
				{t9("reliability.notMeasured")}
			</span>
		);
	}
	if (inline) {
		return (
			<span className="inline-flex items-baseline gap-1" data-testid={`leaderboard-reliability-${entry.rank}`}>
				<ValueCell value={entry.reliability} kind="rate" />
				{known && <span className="text-muted-foreground font-mono text-[11px]">{t("leaderboard.cell.passK", { k: entry.trials })}</span>}
			</span>
		);
	}
	if (entry.trials < 2) {
		return (
			<div className="flex flex-col items-end gap-0.5" data-testid={`leaderboard-reliability-${entry.rank}`}>
				<span
					className="bench-hatched text-muted-foreground rounded-sm border border-dashed px-1 text-xs"
					data-state="unknown"
					title={entry.reliability.reason}
				>
					{t9("reliability.notMeasured")}
				</span>
				<span className="text-muted-foreground text-[11px]">{t9("reliability.needsTries", { k: entry.trials })}</span>
			</div>
		);
	}
	return (
		<div className="flex flex-col items-end gap-0.5" data-testid={`leaderboard-reliability-${entry.rank}`}>
			<ValueCell value={entry.reliability} kind="rate" />
			{known && <span className="text-muted-foreground font-mono text-[11px]">{t("leaderboard.cell.passK", { k: entry.trials })}</span>}
		</div>
	);
}

/** Signed points under a fault minus clean. Absent: the config did not run that condition, which is not an unknown. */
export function ResilienceCell({ value, condition, testId }: { value: Value | undefined; condition: string; testId: string }) {
	const { t } = useTranslation("bench");
	if (!value) {
		return <EmptyMark label={t("leaderboard.cell.notRun")} hint={t("leaderboard.cell.notRunHint", { condition })} testId={testId} />;
	}
	const points = observedNumber(value);
	if (points === null) return <ValueCell value={value} kind="rate" testId={testId} />;
	const tone = resilienceTone(points);
	// The arrow repeats the sign for readers who cannot tell the colours apart.
	const Arrow = tone === "good" ? ArrowUpRight : tone === "bad" ? ArrowDownRight : null;
	return (
		<span
			className={cn("inline-flex items-center justify-end gap-0.5 font-medium tabular-nums", TONE_TEXT[tone])}
			data-testid={testId}
			data-tone={tone}
		>
			{t("leaderboard.cell.points", { value: formatSignedPp(points, getLocale()) })}
			{Arrow && <Arrow className="size-3.5 shrink-0" aria-hidden="true" />}
		</span>
	);
}

/** Links to each contributing experiment, newest first; long lists are cut. */
export function ExperimentLinks({ entry, max = 2 }: { entry: Pick<LeaderboardEntry, "rank" | "experiments">; max?: number }) {
	const { t } = useTranslation("bench");
	const experiments = entry.experiments ?? [];
	const shown = experiments.slice(0, max);
	const rest = experiments.slice(max);
	return (
		<div className="flex flex-col items-start gap-0.5" data-testid={`leaderboard-experiments-${entry.rank}`}>
			{shown.map((id) => (
				<EntityLink key={id} kind="experiment" id={id} className="text-xs" testId={`leaderboard-experiment-${entry.rank}-${id}`} />
			))}
			{rest.length > 0 && (
				<span className="text-muted-foreground text-xs" title={rest.join(", ")}>
					{t("leaderboard.cell.moreExperiments", { count: rest.length })}
				</span>
			)}
		</div>
	);
}