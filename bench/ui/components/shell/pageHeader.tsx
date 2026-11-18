import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";

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
	className?: string;
	testId?: string;
}

/**
 * The top of every page: an optional breadcrumb trail, the title with its badges, a one-line
 * subtitle and the page's actions. Pages use it instead of their own h1 block so every page
 * starts the same way.
 */
export function PageHeader({ title, subtitle, actions, meta, breadcrumbs, className, testId }: PageHeaderProps) {
	const { t } = useTranslation("bench");
	return (
		<header className={cn("flex min-w-0 flex-col gap-1.5", className)} data-testid={testId ?? "page-header"}>
			{breadcrumbs && breadcrumbs.length > 0 && (
				<nav aria-label={t("shell.breadcrumbs")} className="min-w-0">
					<ol className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-1 text-xs">
						{breadcrumbs.map((crumb, index) => {
							const last = index === breadcrumbs.length - 1;
							return (
								<Fragment key={index}>
									<li className="min-w-0 truncate">
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
									</li>
									{!last && (
										<li aria-hidden="true" className="flex items-center">
											<ChevronRight className="size-3 opacity-60" />
										</li>
									)}
								</Fragment>
							);
						})}
					</ol>
				</nav>
			)}
			<div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
				<h1 className="text-foreground min-w-0 text-xl font-semibold tracking-tight break-words">{title}</h1>
				{meta && <div className="flex flex-wrap items-center gap-2">{meta}</div>}
				{actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
			</div>
			{subtitle && <p className="text-muted-foreground max-w-3xl text-sm">{subtitle}</p>}
		</header>
	);
}