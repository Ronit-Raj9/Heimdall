import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import type { Mode } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";

const MODE_VARIANT = {
	live: "success",
	recorded: "default",
	synthetic: "warning",
} as const satisfies Record<Mode, "success" | "default" | "warning">;

const MODE_HINT_KEY = {
	live: "mode.liveHint",
	recorded: "mode.recordedHint",
	synthetic: "mode.syntheticHint",
} as const satisfies Record<Mode, string>;

// The dot repeats the mode's colour; its shape differs per mode so the badge reads without colour.
const MODE_DOT = {
	live: "rounded-full bg-chart-success",
	recorded: "rounded-full border-[1.5px] border-current bg-transparent",
	synthetic: "rotate-45 rounded-[1px] bg-chart-warning",
} as const satisfies Record<Mode, string>;

export interface ModeBadgeProps {
	/** The `mode` field of an API response. */
	mode: Mode | undefined;
	isLoading?: boolean;
	/** The server did not answer, so the mode is not known. */
	isError?: boolean;
	className?: string;
	/** "lg" for the app header, where the mode must be seen first. */
	size?: "default" | "lg";
}

/** LIVE, RECORDED or SYNTHETIC, from the `mode` every bench response carries (DESIGN.md 16.7). */
export function ModeBadge({ mode, isLoading = false, isError = false, className, size = "default" }: ModeBadgeProps) {
	const { t } = useTranslation("bench");
	const lg = size === "lg";

	if (isLoading) {
		return (
			<Skeleton className={cn(lg ? "h-7 w-28" : "h-5 w-20", className)} aria-label={t("mode.loading")} data-testid="mode-badge-loading" />
		);
	}

	if (!mode || isError) {
		return (
			<Badge
				variant="destructive"
				className={cn("font-mono tracking-wide", lg && "h-7 px-2.5 text-xs", className)}
				title={t("mode.offlineHint")}
				data-testid="mode-badge-offline"
			>
				<span aria-hidden="true" className="bg-chart-error size-1.5 rounded-full" />
				<span>{t("mode.offline")}</span>
			</Badge>
		);
	}

	const hint = t(MODE_HINT_KEY[mode]);
	return (
		<Badge
			variant={MODE_VARIANT[mode]}
			className={cn("gap-1.5 font-mono font-semibold tracking-[0.06em]", lg ? "h-7 px-2.5 text-xs" : "text-[11px]", className)}
			title={hint}
			aria-label={hint}
			data-mode={mode}
			data-testid={`mode-badge-${mode}`}
		>
			<span aria-hidden="true" className={cn("inline-block size-1.5 shrink-0", MODE_DOT[mode])} />
			<span>{t(`mode.${mode}`)}</span>
		</Badge>
	);
}