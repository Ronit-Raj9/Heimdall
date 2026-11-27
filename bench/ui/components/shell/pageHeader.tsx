import { OverflowMenu, type OverflowMenuItem } from "@/components/agents/overflowMenu";
import { activeNavItem, BENCH_NAV } from "@/lib/constants/navigation";
import { cn } from "@/lib/utils";
import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import type { ComponentType, ReactNode } from "react";
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
	/**
	 * Secondary actions (and Delete, always) in a "More" menu after the actions (Spec 3). Keep the
	 * primary action and Share in `actions`; with more than three actions move the rest here.
	 */
	more?: OverflowMenuItem[];
	/** Label of the More button; defaults to "More actions". */
	moreLabel?: string;
	/** The page's RouteTabs: rendered right under the title row, so the h1 is always above the tabs. */
	tabs?: ReactNode;
	breadcrumbs?: Crumb[];
	/**
	 * @deprecated No icon is drawn before the title (X-20): the sidebar already shows it. Kept so
	 * callers compile; page lanes drop it.
	 */
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
	let list = [...crumbs];
	// Home is never a parent worth a crumb: the top bar and the sidebar already lead there.
	if (list[0].to === "/") list = list.slice(1);
	if (list.length === 0) return [];
	const [first, ...rest] = list;
	if (sectionPath && first.to === sectionPath && rest.length > 0) return rest.length > 1 ? rest : [];
	// Only the current page left: no trail.
	if (list.length === 1 && !list[0].to) return [];
	return list;
}

/** The sidebar label key of a crumb that points at a sidebar entry, so crumbs and sidebar use one word. */
export function crumbNavLabelKey(crumb: Crumb): string | null {
	if (!crumb.to) return null;
	return BENCH_NAV.find((item) => item.to === crumb.to)?.labelKey ?? null;
}

/**
 * The top of every page: an optional breadcrumb trail, the title with its badges, a one-line
 * subtitle and the page's actions. Pages use it instead of their own h1 block so every page
 * starts the same way.
 */
export function PageHeader({ title, subtitle, actions, meta, more, moreLabel, tabs, breadcrumbs, className, testId }: PageHeaderProps) {
	const { t } = useTranslation("bench");
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
												className="hover:text-foreground rounded-sm transition-colors focus-visible:ring-2 focus-visible:ring-(--focus) focus-visible:outline-none"
											>
												{crumbNavLabelKey(crumb) ? t(crumbNavLabelKey(crumb) as never) : crumb.label}
											</Link>
										) : (
											<span aria-current={last ? "page" : undefined} className={cn(last && "text-foreground")}>
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
			{/* The title never breaks inside a word; below md the actions take their own row under it. */}
			<div className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-2">
				<div className="flex min-w-0 basis-full flex-col gap-1 md:min-w-[18rem] md:flex-1 md:basis-0">
					<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
						<Heading
							className={cn(
								"text-foreground min-w-0 font-semibold tracking-tight text-balance hyphens-none [overflow-wrap:normal]",
								Heading === "h1" ? "text-[22px] leading-7" : "text-xl",
							)}
						>
							{title}
						</Heading>
						{meta && <div className="flex flex-wrap items-center gap-2">{meta}</div>}
					</div>
					{subtitle && <p className="text-muted-foreground max-w-2xl text-sm leading-relaxed text-pretty">{subtitle}</p>}
				</div>
				{(actions || (more && more.length > 0)) && (
					<div className="flex w-full flex-wrap items-center gap-2 md:ml-auto md:w-auto" data-testid="page-header-actions">
						{actions}
						{more && more.length > 0 && <OverflowMenu label={moreLabel ?? t("shell.moreActions")} items={more} testId="page-header-more" />}
					</div>
				)}
			</div>
			{tabs && <div className="mt-1 min-w-0">{tabs}</div>}
		</header>
	);
}
