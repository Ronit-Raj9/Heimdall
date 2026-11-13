import type { Outcome } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { outcomeTone } from "./runs.utils";

const TONE_CLASS = {
	pass: "border-chart-success/50 bg-chart-success/10 text-chart-success-ink",
	zero: "border-chart-error/50 bg-chart-error/10 text-chart-error-ink",
	unknown: "bench-hatched text-muted-foreground border-dashed",
} as const;

/** A run outcome, coloured by how the verifier scores it: pass 1, the zero outcomes 0, the rest unknown. */
export function OutcomeBadge({ outcome, className, testId }: { outcome: Outcome; className?: string; testId?: string }) {
	const { t } = useTranslation("bench");
	const tone = outcomeTone(outcome);
	return (
		<span
			data-testid={testId ?? `outcome-${outcome}`}
			data-outcome={outcome}
			className={cn("inline-flex w-fit items-center rounded-sm border px-1.5 py-0.5 text-xs font-medium", TONE_CLASS[tone], className)}
		>
			{t(`runs.outcome.${outcome}`)}
		</span>
	);
}