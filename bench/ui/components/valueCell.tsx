import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getLocale } from "@/lib/i18n";
import type { Value, ValueKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { describeValue } from "./valueCell.utils";

export interface ValueCellProps {
	value: Value | null | undefined;
	/** Renderer for the metric's unit; comes from the catalog's metric descriptor. */
	kind: ValueKind;
	className?: string;
	/** Overrides the default data-testid ("value-cell-<state>"). */
	testId?: string;
}

/**
 * The one renderer for every measured number (DESIGN.md 8.1 rule 6, 16.2).
 * Observed: the formatted number. Unknown: hatched, never zero, with the reason and the bounds
 * in a tooltip and the accessible name. Not applicable: "n/a".
 */
export function ValueCell({ value, kind, className, testId }: ValueCellProps) {
	const { t } = useTranslation("bench");
	const view = describeValue(value, kind, getLocale());

	if (view.state === "observed") {
		return (
			<span
				data-slot="value-cell"
				data-state="observed"
				data-kind={kind}
				data-evidence={view.evidence}
				data-testid={testId ?? "value-cell-observed"}
				className={cn("tabular-nums", className)}
			>
				{view.text}
			</span>
		);
	}

	if (view.state === "not_applicable") {
		const hint = t("value.notApplicableHint");
		return (
			<span
				data-slot="value-cell"
				data-state="not_applicable"
				data-kind={kind}
				data-testid={testId ?? "value-cell-not-applicable"}
				aria-label={hint}
				title={hint}
				className={cn("text-muted-foreground", className)}
			>
				{t("value.notApplicable")}
			</span>
		);
	}

	const reason = view.reason ?? (value ? t("value.noReason") : t("value.missing"));
	const hint = view.bounds
		? t("value.unknownBoundsHint", { reason, lo: view.bounds[0], hi: view.bounds[1] })
		: t("value.unknownHint", { reason });
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span
					data-slot="value-cell"
					data-state="unknown"
					data-kind={kind}
					data-testid={testId ?? "value-cell-unknown"}
					tabIndex={0}
					aria-label={hint}
					className={cn(
						"bench-hatched text-muted-foreground focus-visible:ring-ring/50 inline-flex cursor-help items-center rounded-sm border border-dashed px-1.5 text-xs italic focus-visible:ring-[3px]",
						className,
					)}
				>
					{t("value.unknown")}
				</span>
			</TooltipTrigger>
			<TooltipContent>{hint}</TooltipContent>
		</Tooltip>
	);
}