import { activeNavItem } from "@/lib/constants/navigation";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { isValidElement, type ComponentType, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useEmbedded } from "./embedded";

/** One step of the trail above the title. The last crumb is the current page and is not a link. */
export interface Crumb {
	label: ReactNode;
	/** A route path; leave it out for a crumb that is text only. */
	to?: string;
	params?: Record<string, string>;
	search?: Record<string, string>;
}

export interface PageHeaderProps {
	title: ReactNode;
	/** One sentence, never more. */
	subtitle?: ReactNode;
	/** Buttons and links on the right of the title; they wrap under it on a narrow screen. */
	actions?: ReactNode;
	/** Badges beside the title (data mode, grading). */
	meta?: ReactNode;
	breadcrumbs?: Crumb[];
	/** Optional small muted icon before the title: a lucide component or a rendered node. */
	icon?: ComponentType<{ className?: string }> | ReactNode;
	className?: string;
	testId?: string;
}

/** The path of the section the top bar shows for this page (its nav entry), or null without a router. */
function useSectionPath(): string | null {
	try {
		const pathname = useRouterState({ select: (state) => state.location.pathname });
		return activeNavItem(pathname)?.to ?? null;
	} catch {
		// Rendered without a router (an isolated component test): keep every crumb.
		return null;
	}
}

/**
 * The crumbs the page shows: a leading crumb that links to the section the top bar already shows is
 * dropped, and a trail left with only the current page is not shown (N203). Pure.
 */
export function pageCrumbs(crumbs: readonly Crumb[] | undefined, sectionPath: string | null): Crumb[] {
	if (!crumbs || crumbs.length === 0) return [];
	const [first, ...rest] = crumbs;
	if (sectionPath && first.to === sectionPath && rest.length > 0) return rest.length > 1 ? rest : [];
	return [...crumbs];
}

/**
 * The top of every page: an optional breadcrumb trail, the title with its badges, a one-line
 * subtitle and the page's actions. Pages use it instead of their own h1 block so every page
 * starts the same way.
 */
export function PageHeader({ title, subtitle, actions, meta, breadcrumbs, icon, className, testId }: PageHeaderProps) {
	const { t } = useTranslation("bench");
	const iconNode = renderIcon(icon);
	// Inside another page's tab the title is a section heading, not the page's h1.
	const Heading = useEmbedded() ? "h2" : "h1";
	const trail = pageCrumbs(breadcrumbs, useSectionPath());
	return (
		<header className={cn("mb-2 flex min-w-0 flex-col gap-2", className)} data-testid={testId ?? "page-header"}>
			{trail.length > 0 && (
				<nav aria-label={t("shell.breadcrumbs")} className="min-w-0">
					<ol className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-1 text-xs">
						{trail.map((crumb, index) => {
							const last = index === trail.length - 1;
							// The chevron sits inside the item after the first, so it is only ever between two items.
							return (
								<li key={index} className="flex min-w-0 items-center gap-1">
									{index > 0 && <ChevronRight aria-hidden="true" className="size-3 shrink-0 opacity-60" />}
									<span className="min-w-0 truncate">
										{crumb.to && !last ? (
											<Link
												to={crumb.to as never}
												params={crumb.params as never}
												search={crumb.search as never}
												className="hover:text-foreground focus-visible:ring-ring/60 rounded-sm transition-colors focus-visible:ring-2 focus-visible:outline-none"
											>
												{crumb.label}
											</Link>
										) : (
											<span aria-current={last ? "page" : undefined} className={cn(last && "text-foreground/80")}>
												{crumb.label}
											</span>
										)}
									</span>
								</li>
							);
						})}
					</ol>
				</nav>
			)}
			<div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-3">
				{iconNode && (
					<span aria-hidden="true" className="text-muted-foreground mt-1.5 flex shrink-0 items-center" data-testid="page-header-icon">
						{iconNode}
					</span>
				)}
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
						<Heading
							className={cn("text-foreground min-w-0 font-semibold tracking-tight break-words", Heading === "h1" ? "text-2xl" : "text-xl")}
						>
							{title}
						</Heading>
						{meta && <div className="flex flex-wrap items-center gap-2">{meta}</div>}
					</div>
					{subtitle && <p className="text-muted-foreground max-w-2xl text-sm leading-relaxed">{subtitle}</p>}
				</div>
				{actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
			</div>
		</header>
	);
}

function renderIcon(icon: PageHeaderProps["icon"]): ReactNode {
	if (icon === undefined || icon === null || icon === false) return null;
	if (isValidElement(icon)) return icon;
	// A component: lucide icons are forwardRef objects, plain icons are functions.
	if (typeof icon === "function" || (typeof icon === "object" && "$$typeof" in (icon as object))) {
		const Icon = icon as ComponentType<{ className?: string }>;
		return <Icon className="size-4" />;
	}
	return icon as ReactNode;
}