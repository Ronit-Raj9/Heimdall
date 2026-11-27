import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

/**
 * The bar that keeps a long form's summary and primary button in view (M1-50): sticks to the
 * bottom of the scrolling area, buttons right aligned; on phones it wraps.
 */
export function StickyFooter({ children, className, testId }: { children: ReactNode; className?: string; testId?: string }) {
	return (
		<div
			className={cn("bg-card sticky bottom-0 z-10 flex flex-wrap items-center justify-end gap-2 border-t px-4 py-3", className)}
			data-testid={testId ?? "sticky-footer"}
		>
			{children}
		</div>
	);
}
