import { cn } from "@/lib/utils";
import { ChevronRight, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

export interface DisclosureProps {
	title: string;
	icon?: LucideIcon;
	/** Short facts beside the title, visible while closed (a count, a status pill). */
	meta?: ReactNode;
	/** Right-aligned control in the summary row, for example the grade source tag. */
	action?: ReactNode;
	defaultOpen?: boolean;
	testId: string;
	children: ReactNode;
}

/**
 * A collapsed section of the verdict: the title row is always visible, the body opens on click.
 * A native details element, so the content stays in the markup (search, print, tests) while closed.
 */
export function Disclosure({ title, icon: Icon, meta, action, defaultOpen = false, testId, children }: DisclosureProps) {
	return (
		<details
			className="group bg-card rounded-xl border transition-shadow open:shadow-sm hover:shadow-sm"
			open={defaultOpen}
			data-testid={testId}
		>
			<summary className="flex cursor-pointer list-none items-center gap-3 px-5 py-4 select-none [&::-webkit-details-marker]:hidden">
				<ChevronRight className="text-muted-foreground size-4 shrink-0 transition-transform group-open:rotate-90" aria-hidden="true" />
				{Icon && <Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />}
				<span className="text-sm font-semibold tracking-tight">{title}</span>
				{meta && <span className="text-muted-foreground flex min-w-0 items-center gap-2 text-xs">{meta}</span>}
				{action && <span className={cn("ml-auto flex items-center")}>{action}</span>}
			</summary>
			<div className="overflow-x-auto border-t px-5 py-4">{children}</div>
		</details>
	);
}
