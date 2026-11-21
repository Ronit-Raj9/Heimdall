import type { ReplayLabel } from "@/lib/types/runDebug";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const TONE: Record<ReplayLabel, string> = {
	hit: "bg-muted text-muted-foreground",
	edited: "bg-primary/10 text-primary",
	divergence: "bg-chart-error/15 text-chart-error-ink",
	exhausted: "bg-chart-warning/15 text-chart-warning-ink",
};

/** How a replayed model call was answered: from the recording, edited, or different from it. */
export function ReplayBadge({ label, testId }: { label: ReplayLabel; testId?: string }) {
	const { t } = useTranslation("runDebug");
	return (
		<span className={cn("rounded-full px-2 py-0.5 text-[11px] font-medium", TONE[label])} data-testid={testId} data-replay={label}>
			{t(`replay.${label}`)}
		</span>
	);
}
