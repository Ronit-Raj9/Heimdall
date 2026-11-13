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

export interface ModeBadgeProps {
	/** The `mode` field of an API response. */
	mode: Mode | undefined;
	isLoading?: boolean;
	/** The server did not answer, so the mode is not known. */
	isError?: boolean;
	className?: string;
}

/** LIVE, RECORDED or SYNTHETIC, from the `mode` every bench response carries (DESIGN.md 16.7). */
export function ModeBadge({ mode, isLoading = false, isError = false, className }: ModeBadgeProps) {
	const { t } = useTranslation("bench");

	if (isLoading) {
		return <Skeleton className={cn("h-5 w-20", className)} aria-label={t("mode.loading")} data-testid="mode-badge-loading" />;
	}

	if (!mode || isError) {
		return (
			<Badge variant="destructive" className={className} title={t("mode.offlineHint")} data-testid="mode-badge-offline">
				{t("mode.offline")}
			</Badge>
		);
	}

	const hint = t(MODE_HINT_KEY[mode]);
	return (
		<Badge
			variant={MODE_VARIANT[mode]}
			className={cn("font-mono tracking-wide", className)}
			title={hint}
			aria-label={hint}
			data-mode={mode}
			data-testid={`mode-badge-${mode}`}
		>
			{t(`mode.${mode}`)}
		</Badge>
	);
}