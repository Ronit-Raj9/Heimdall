import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Inbox, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

/** The one thing to do next: a route to open, or a handler to call. */
export type EmptyStateAction =
	| { label: ReactNode; to: string; params?: Record<string, string>; search?: Record<string, string>; onClick?: never }
	| { label: ReactNode; onClick: () => void; to?: never; params?: never; search?: never };

export interface EmptyStateProps {
	icon?: LucideIcon;
	/** One sentence: what is missing and why. */
	message: ReactNode;
	action?: EmptyStateAction;
	/** Smaller padding, for a panel inside a page. */
	compact?: boolean;
	className?: string;
	testId?: string;
}

/** What a page or panel shows when it has nothing yet: an icon, one sentence and one action. */
export function EmptyState({ icon: Icon = Inbox, message, action, compact, className, testId }: EmptyStateProps) {
	return (
		<div
			className={cn(
				"border-border/80 flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed text-center",
				compact ? "px-4 py-6" : "px-6 py-12",
				className,
			)}
			data-testid={testId ?? "empty-state"}
		>
			<span className="bg-muted text-muted-foreground flex size-9 items-center justify-center rounded-full">
				<Icon className="size-4.5" aria-hidden="true" />
			</span>
			<p className="text-muted-foreground max-w-md text-sm">{message}</p>
			{action &&
				(action.to !== undefined ? (
					<Button asChild size="sm" variant="outline">
						<Link to={action.to as never} params={action.params as never} search={action.search as never}>
							{action.label}
						</Link>
					</Button>
				) : (
					<Button size="sm" variant="outline" onClick={action.onClick}>
						{action.label}
					</Button>
				))}
		</div>
	);
}