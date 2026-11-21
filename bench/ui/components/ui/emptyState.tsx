import { cn } from "@/lib/utils";
import { Inbox, type LucideIcon } from "lucide-react";
import * as React from "react";

export interface EmptyStateProps {
	title: React.ReactNode;
	/** One sentence. */
	description?: React.ReactNode;
	icon?: LucideIcon;
	/** One button. */
	action?: React.ReactNode;
	/** Smaller padding, for inside cards and table bodies. */
	compact?: boolean;
	className?: string;
	testId?: string;
}

/** Icon in a soft circle, one title, one line, one action. */
export function EmptyState({ title, description, icon: Icon = Inbox, action, compact = false, className, testId }: EmptyStateProps) {
	return (
		<div
			data-slot="empty-state"
			data-testid={testId}
			className={cn(
				"flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed border-border/80 text-center",
				compact ? "px-4 py-8" : "px-6 py-14",
				className,
			)}
		>
			<span className="bg-muted text-muted-foreground ring-muted/40 flex size-11 items-center justify-center rounded-full ring-8">
				<Icon className="size-5" aria-hidden="true" />
			</span>
			<div className="flex max-w-sm flex-col gap-1">
				<p className="text-foreground text-sm font-semibold">{title}</p>
				{description && <p className="text-muted-foreground text-sm text-pretty">{description}</p>}
			</div>
			{action && <div className="mt-1">{action}</div>}
		</div>
	);
}