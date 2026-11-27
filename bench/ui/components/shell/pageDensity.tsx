import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

export type Density = "compact" | "default" | "comfortable";

/**
 * Sets the density scale for a page (Spec 7): rows, control heights, card padding and gaps of the
 * shared primitives inside read the --row-h, --control-h, --card-pad, --gap and --section-gap
 * tokens of app/globals.css. Lists and pickers use compact, forms default, landing and sign-in
 * comfortable. The wrapper is a plain block and adds no spacing of its own.
 */
export function PageDensity({ density, children, className }: { density: Density; children: ReactNode; className?: string }) {
	return (
		<div data-density={density} className={cn("contents", className)} data-testid="page-density">
			{children}
		</div>
	);
}
