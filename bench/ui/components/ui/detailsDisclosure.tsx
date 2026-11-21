import { cn } from "@/lib/utils";
import { ChevronRight } from "lucide-react";
import * as React from "react";
import { useTranslation } from "react-i18next";

export interface DetailsDisclosureProps {
	children: React.ReactNode;
	/** Summary text; defaults to "Details". */
	label?: React.ReactNode;
	/** Muted text after the label (e.g. "4 items"). */
	meta?: React.ReactNode;
	defaultOpen?: boolean;
	className?: string;
	contentClassName?: string;
	testId?: string;
}

/**
 * Detail on demand: a collapsed "Details" row with a chevron. Built on native details/summary, so
 * it is keyboard accessible and works without JS. Put hashes, paths and versions here.
 */
export function DetailsDisclosure({ children, label, meta, defaultOpen, className, contentClassName, testId }: DetailsDisclosureProps) {
	const { t } = useTranslation("ui");
	return (
		<details data-slot="details-disclosure" data-testid={testId} open={defaultOpen} className={cn("group/details min-w-0", className)}>
			<summary className="text-muted-foreground hover:text-foreground focus-visible:ring-ring/50 inline-flex cursor-pointer list-none items-center gap-1 rounded-md text-sm font-medium transition-colors select-none focus-visible:ring-2 focus-visible:outline-none [&::-webkit-details-marker]:hidden">
				<ChevronRight className="size-4 transition-transform duration-150 group-open/details:rotate-90" aria-hidden="true" />
				<span>{label ?? t("details")}</span>
				{meta && <span className="text-muted-foreground/80 font-normal">{meta}</span>}
			</summary>
			<div className={cn("mt-3 min-w-0", contentClassName)}>{children}</div>
		</details>
	);
}