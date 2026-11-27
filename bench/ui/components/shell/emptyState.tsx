import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { Inbox, SearchX, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

/** The one thing to do next: a route to open, or a handler to call. */
export type EmptyStateAction =
	| { label: ReactNode; to: string; params?: Record<string, string>; search?: Record<string, unknown>; onClick?: never }
	| { label: ReactNode; onClick: () => void; to?: never; params?: never; search?: never };

/** A quieter second way forward: a route or an outside link. */
export type EmptyStateSecondary = { label: ReactNode; to: string; href?: never } | { label: ReactNode; href: string; to?: never };

export interface EmptyStateProps {
	/** first: nothing exists yet (filled action). filtered: a filter hides everything (outline "Clear filters"). */
	kind?: "first" | "filtered";
	icon?: LucideIcon;
	/** Five words or fewer. */
	title?: ReactNode;
	/** One sentence: what it is and why it is empty. */
	body?: ReactNode;
	/** The old one-sentence prop; read as body. */
	message?: ReactNode;
	action?: EmptyStateAction;
	secondary?: EmptyStateSecondary;
	/** Smaller padding, for a panel inside a page. */
	compact?: boolean;
	className?: string;
	testId?: string;
}

/**
 * The one empty state of the app (Spec 6): icon, a short title, one sentence, one action and an
 * optional quiet second link. Never shown while loading or after a failed load (errors are
 * LoadError). For a filtered list pass kind="filtered" and a "Clear filters" action.
 */
export function EmptyState({ kind = "first", icon, title, body, message, action, secondary, compact, className, testId }: EmptyStateProps) {
	const Icon = icon ?? (kind === "filtered" ? SearchX : Inbox);
	const text = body ?? message;
	const variant = kind === "filtered" ? "outline" : "default";
	return (
		<div
			className={cn(
				"border-border/80 flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed text-center",
				compact ? "px-4 py-6" : "px-6 py-12",
				className,
			)}
			data-testid={testId ?? "empty-state"}
			data-kind={kind}
		>
			<span className="bg-muted text-muted-foreground flex size-10 items-center justify-center rounded-full">
				<Icon className="size-5" aria-hidden="true" />
			</span>
			{title && <p className="text-foreground text-sm font-semibold">{title}</p>}
			{text && <p className="text-muted-foreground max-w-md text-sm text-pretty">{text}</p>}
			{action &&
				(action.to !== undefined ? (
					<Button asChild size="sm" variant={variant}>
						<Link to={action.to as never} params={action.params as never} search={action.search as never}>
							{action.label}
						</Link>
					</Button>
				) : (
					<Button size="sm" variant={variant} onClick={action.onClick}>
						{action.label}
					</Button>
				))}
			{secondary &&
				(secondary.to !== undefined ? (
					<Link to={secondary.to as never} className="text-primary text-xs hover:underline">
						{secondary.label}
					</Link>
				) : (
					<a href={secondary.href} className="text-primary text-xs hover:underline" target="_blank" rel="noreferrer">
						{secondary.label}
					</a>
				))}
		</div>
	);
}
