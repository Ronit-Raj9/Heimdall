import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getLocale } from "@/lib/i18n";
import type { Value, ValueKind } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { describeValue } from "./valueCell.utils";

export interface ValueCellProps {
	value: Value | null | undefined;
	/** Renderer for the metric's unit; comes from the catalog's metric descriptor. */
	kind: ValueKind;
	className?: string;
	/** Overrides the default data-testid ("value-cell-<state>"). */
	testId?: string;
	/**
	 * False keeps an observed cell out of the tab order (a list row whose link is the focus stop);
	 * its source stays readable through the screen-reader text. Default true.
	 */
	focusable?: boolean;
}

/**
 * The one renderer for every measured number (DESIGN.md 8.1 rule 6, 16.2).
 * Observed: the formatted number. Unknown: hatched, never zero, with the reason and the bounds
 * in a tooltip and the accessible name. Not applicable: "n/a".
 */
export function ValueCell({ value, kind, className, testId, focusable = true }: ValueCellProps) {
	const { t } = useTranslation("bench");
	const view = describeValue(value, kind, getLocale());

	if (view.state === "observed") {
		// The evidence level is readable on hover and by assistive technology, not only in a data
		// attribute, so a bare number still says where it was measured (DESIGN.md 8.1 rule 6).
		const provenance = view.evidence ? t(`value.evidence.${view.evidence}`, { defaultValue: view.evidence }) : undefined;
		const cell = (
			<span
				data-slot="value-cell"
				data-state="observed"
				data-kind={kind}
				data-evidence={view.evidence}
				data-testid={testId ?? "value-cell-observed"}
				title={provenance}
				// A focus stop and a tap target too, so sighted keyboard and touch users can reveal the
				// source, as the unknown cell's trigger does (QC2-29-08).
				tabIndex={provenance ? (focusable ? 0 : -1) : undefined}
				className={cn(
					"tabular-nums",
					provenance && "focus-visible:ring-ring/50 cursor-help rounded-[2px] focus-visible:ring-[3px] focus-visible:outline-none",
					className,
				)}
			>
				{view.text}
				{provenance && <span className="sr-only"> ({provenance})</span>}
			</span>
		);
		return provenance ? <ObservedProvenance cell={cell} provenance={provenance} /> : cell;
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
				className={cn("text-muted-foreground/70 inline-flex items-center", className)}
			>
				{/* A quiet rule, not repeated words; the text stays for screen readers. Never a zero. */}
				<span aria-hidden="true" className="inline-block h-px w-3 rounded-full bg-current opacity-70" />
				<span className="sr-only">{t("value.notApplicable")}</span>
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
						"bench-hatched text-muted-foreground focus-visible:ring-ring/50 inline-flex h-5 cursor-help items-center rounded-[4px] border border-dashed px-1.5 text-[11px] leading-none focus-visible:ring-[3px]",
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

/**
 * The observed cell's source in a tooltip that opens on hover, keyboard focus and a tap (Radix
 * tooltips ignore touch, so the tap toggles it).
 */
function ObservedProvenance({ cell, provenance }: { cell: React.ReactElement; provenance: string }) {
	const [open, setOpen] = useState(false);
	return (
		<Tooltip open={open} onOpenChange={setOpen}>
			<TooltipTrigger asChild onClick={() => setOpen((was) => !was)}>
				{cell}
			</TooltipTrigger>
			<TooltipContent data-testid="value-cell-provenance">{provenance}</TooltipContent>
		</Tooltip>
	);
}
