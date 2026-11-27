import { cn } from "@/lib/utils";
import type { ReactNode } from "react";

/**
 * The page width kinds (Q-317). wide: tables and lists (full width up to 1680px). dash: dashboards
 * and result pages such as Home, an agent, an evaluation result or a comparison (up to 1440px).
 * read: forms and reading such as a docs article, the wizard or settings forms (up to 1000px).
 */
export type PageWidthKind = "wide" | "dash" | "read";

const CLASS: Readonly<Record<PageWidthKind, string>> = {
	wide: "max-w-(--page-max-wide)",
	dash: "max-w-(--page-max-dash)",
	read: "max-w-(--page-max-read)",
};

/** The classes that size a page block of this kind: full width, centred, capped. Pure. */
export function pageWidthClass(kind: PageWidthKind): string {
	return cn("mx-auto w-full min-w-0", CLASS[kind]);
}

/** Wraps a page's content (its PageHeader included) so header and body share one width. */
export function PageWidth({ kind, className, children, testId }: { kind: PageWidthKind; className?: string; children: ReactNode; testId?: string }) {
	return (
		<div className={cn(pageWidthClass(kind), className)} data-page-width={kind} data-testid={testId}>
			{children}
		</div>
	);
}
