import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import "@/components/runs/screenStatesI18n";

export type NotMeasuredReason = "notMeasured" | "notRun" | "notTested" | "noModelCall" | "notApplicable";

/**
 * A value that was never measured, so it can never be read as 0 (Spec 12): muted words on the
 * hatched background. notApplicable renders "n/a" with its meaning in the tooltip. A 0 is only
 * for something measured whose count is zero ("0 of 6 runs").
 */
export function NotMeasured({ reason = "notMeasured", className, testId }: { reason?: NotMeasuredReason; className?: string; testId?: string }) {
	const { t } = useTranslation("ufs2");
	const text = t(`notMeasured.${reason}` as never);
	const hint = t(`notMeasured.hint.${reason}` as never);
	return (
		<span
			className={cn("bench-hatched text-muted-foreground inline-flex items-center rounded px-1.5 py-0.5 text-xs whitespace-nowrap", className)}
			title={hint}
			data-testid={testId}
			data-not-measured={reason}
		>
			{text}
		</span>
	);
}
