import { cn } from "@/lib/utils";
import * as React from "react";

export interface SectionProps {
	title?: React.ReactNode;
	description?: React.ReactNode;
	/** Buttons on the right of the heading. */
	actions?: React.ReactNode;
	children?: React.ReactNode;
	className?: string;
	testId?: string;
	/** Heading level; default h2. Pass h3 inside a tab panel that sits under an h2, so levels never skip. */
	as?: "h2" | "h3" | "h4";
}

/** A titled block of a page: heading, one-line description, actions on the right, body. */
export function Section({ title, description, actions, children, className, testId, as: Heading = "h2" }: SectionProps) {
	const hasHeader = title || description || actions;
	return (
		<section data-slot="section" data-testid={testId} className={cn("flex min-w-0 flex-col gap-4", className)}>
			{hasHeader && (
				<div className="flex flex-wrap items-end justify-between gap-3">
					<div className="flex min-w-0 flex-col gap-1">
						{title && <Heading className="text-foreground text-base font-semibold tracking-tight">{title}</Heading>}
						{description && <p className="text-muted-foreground max-w-prose text-sm">{description}</p>}
					</div>
					{actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
				</div>
			)}
			{children}
		</section>
	);
}

export interface PageHeaderProps {
	/** 2-4 words. */
	title: React.ReactNode;
	/** One line, about 12 words max. */
	subtitle?: React.ReactNode;
	/** Leading icon or CategoryIcon. */
	icon?: React.ReactNode;
	/** At most one primary button, plus ghost buttons. */
	actions?: React.ReactNode;
	/** Small row above the title (breadcrumb, back link). */
	eyebrow?: React.ReactNode;
	className?: string;
	testId?: string;
}

/** Page title block: large title, one-line subtitle, actions top right. Wraps on phones. */
export function PageHeader({ title, subtitle, icon, actions, eyebrow, className, testId }: PageHeaderProps) {
	return (
		<header data-slot="page-header" data-testid={testId} className={cn("flex flex-col gap-2", className)}>
			{eyebrow && <div className="text-muted-foreground text-sm">{eyebrow}</div>}
			<div className="flex flex-wrap items-start justify-between gap-4">
				<div className="flex min-w-0 items-center gap-3">
					{icon}
					<div className="flex min-w-0 flex-col gap-1">
						<h1 className="text-foreground text-[22px] leading-7 font-semibold tracking-tight text-balance hyphens-none [overflow-wrap:normal]">{title}</h1>
						{subtitle && <p className="text-muted-foreground max-w-prose text-sm text-pretty">{subtitle}</p>}
					</div>
				</div>
				{actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
			</div>
		</header>
	);
}

/** Centered max-width column for list pages (~1200px) with section spacing. */
export function PageContainer({ className, ...props }: React.ComponentProps<"div">) {
	return (
		<div data-slot="page-container" className={cn("mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8", className)} {...props} />
	);
}