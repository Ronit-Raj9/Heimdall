import { Badge } from "@/components/ui/badge";
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

/** Agent config id, model, arm label, short config hash, and a synthetic badge when the runs were scripted. */
export function AgentCell({
	entry,
}: {
	entry: Pick<LeaderboardEntry, "rank" | "agent" | "model" | "label" | "config_hash" | "synthetic">;
}) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex min-w-48 flex-col gap-0.5" data-testid={`leaderboard-agent-${entry.rank}`}>
			<div className="flex items-center gap-1.5">
				<span className="text-foreground font-medium">{entry.agent}</span>
				{entry.synthetic && (
					<Badge
						variant="warning"
						className="px-1 py-0 text-[10px] tracking-wide uppercase"
						title={t("leaderboard.cell.syntheticHint")}
						data-testid={`leaderboard-synthetic-${entry.rank}`}
					>
						{t("leaderboard.cell.synthetic")}
					</Badge>
				)}
			</div>
			<span className="text-muted-foreground font-mono text-xs">{entry.model}</span>
			<div className="text-muted-foreground flex items-center gap-2 text-xs">
				{entry.label && <span className="max-w-56 truncate">{entry.label}</span>}
				<CopyId
					value={entry.config_hash}
					display={shortHash(entry.config_hash)}
					title={t("leaderboard.cell.configHash", { hash: entry.config_hash })}
					className="text-[11px]"
				/>
				<Link to="/agents/diff" search={{ b: entry.config_hash }} className="text-primary hover:underline">
					{t("agentDiff.link.diff")}
				</Link>
			</div>
		</div>
	);
}

/** Verified success as a bar with its 95% interval whiskers, the percent, and passes over graded runs. */
export function SuccessCell({ entry }: { entry: Pick<LeaderboardEntry, "rank" | "success" | "passes"> }) {
	const { t } = useTranslation("bench");
	const locale = getLocale();
	const bar = successBar(entry.success);
	const passes = (
		<span className="text-muted-foreground text-xs tabular-nums">
			{t("leaderboard.cell.passes", { num: entry.passes.num, den: entry.passes.den })}
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

/** pass^k with k = trials. Unknown (fewer than 2 trials) is hatched with its reason. */
export function ReliabilityCell({ entry }: { entry: Pick<LeaderboardEntry, "rank" | "reliability" | "trials"> }) {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col items-end gap-0.5" data-testid={`leaderboard-reliability-${entry.rank}`}>
			<ValueCell value={entry.reliability} kind="rate" />
			<span className="text-muted-foreground font-mono text-[11px]">{t("leaderboard.cell.passK", { k: entry.trials })}</span>
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

/** Links to the verdict of each contributing experiment, newest first; long lists are cut. */
export function ExperimentLinks({ entry, max = 2 }: { entry: Pick<LeaderboardEntry, "rank" | "experiments">; max?: number }) {
	const { t } = useTranslation("bench");
	const experiments = entry.experiments ?? [];
	const shown = experiments.slice(0, max);
	const rest = experiments.slice(max);
	return (
		<div className="flex flex-col items-start gap-0.5" data-testid={`leaderboard-experiments-${entry.rank}`}>
			{shown.map((id) => (
				<Link
					key={id}
					to="/verdict"
					search={{ exp: id }}
					aria-label={t("leaderboard.cell.openVerdict", { id })}
					className="text-primary font-mono text-xs underline-offset-4 hover:underline"
					data-testid={`leaderboard-experiment-${entry.rank}-${id}`}
				>
					{id}
				</Link>
			))}
			{rest.length > 0 && (
				<span className="text-muted-foreground text-xs" title={rest.join(", ")}>
					{t("leaderboard.cell.moreExperiments", { count: rest.length })}
				</span>
			)}
		</div>
	);
}