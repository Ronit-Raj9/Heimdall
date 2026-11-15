import { ToneBadge } from "@/components/suites/suiteBadges";
import type { Grading } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Scale, ShieldCheck } from "lucide-react";
import { useTranslation } from "react-i18next";
import { isOutputKind, isTrack } from "./scenario.utils";

/** A suite's leaderboard track, with what it means. A track this UI does not know shows as sent. */
export function TrackBadge({ track, testId }: { track: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!isTrack(track)) {
		return (
			<ToneBadge tone="neutral" hint={t("scenario.track.otherHint")} testId={testId}>
				{track}
			</ToneBadge>
		);
	}
	return (
		<ToneBadge tone="neutral" hint={t(`scenario.track.${track}Hint`)} testId={testId}>
			{t(`scenario.track.${track}`)}
		</ToneBadge>
	);
}

/** Verified or judged. Judged is the warning tone: its outcomes come from a model, not a check. */
export function GradingBadge({ grading, testId }: { grading: Grading; testId?: string }) {
	const { t } = useTranslation("bench");
	return (
		<ToneBadge
			tone={grading === "judged" ? "warn" : "good"}
			icon={grading === "judged" ? Scale : ShieldCheck}
			hint={t(`scenario.grading.${grading}Hint`)}
			testId={testId}
		>
			{t(`scenario.grading.${grading}`)}
		</ToneBadge>
	);
}

/** Diff, answer or state: what the verifier reads. */
export function OutputBadge({ output, testId }: { output: string; testId?: string }) {
	const { t } = useTranslation("bench");
	if (!isOutputKind(output)) {
		return (
			<ToneBadge tone="neutral" testId={testId}>
				{output}
			</ToneBadge>
		);
	}
	return (
		<ToneBadge tone="neutral" hint={t(`scenario.output.${output}Hint`)} testId={testId}>
			{t(`scenario.output.${output}`)}
		</ToneBadge>
	);
}

/**
 * The plain-words explanation of a judged suite: a pinned judge model decides, and its results are
 * never compared with a verified suite's. Shown wherever a judged suite's outcomes are.
 */
export function JudgedNotice({ title, body, testId, className }: { title?: string; body?: string; testId: string; className?: string }) {
	const { t } = useTranslation("bench");
	return (
		<div
			role="note"
			className={cn("border-chart-warning/50 bg-chart-warning/10 flex items-start gap-2 rounded-sm border px-3 py-2 text-xs", className)}
			data-testid={testId}
		>
			<Scale className="text-chart-warning-ink mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="text-foreground font-medium">{title ?? t("scenario.grading.judgedTitle")}</span>
				<span className="text-muted-foreground">{body ?? t("scenario.grading.judgedBody")}</span>
			</div>
		</div>
	);
}