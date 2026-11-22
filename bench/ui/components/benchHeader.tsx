import { HeimdallMark } from "@/components/heimdallMark";
import { ModeBadge } from "@/components/modeBadge";
import { HelpMenu } from "@/components/platform/helpMenu";
import { JobsIndicator } from "@/components/platform/jobsDrawer";
import { UserMenu } from "@/components/platform/userMenu";
import { ThemeToggle } from "@/components/themeToggle";
import { Button } from "@/components/ui/button";
import { BENCH_NAV_GROUPS, activeNavItem } from "@/lib/constants/navigation";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { IS_FIXTURE_MODE, useGetHealthQuery } from "@/lib/store";
import { launchAvailable } from "@/lib/types/bench";
import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronRight, Menu, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface BenchHeaderProps {
	/** Opens the navigation drawer (below md, where the sidebar is hidden). */
	onOpenNav?: () => void;
	navOpen?: boolean;
}

/**
 * Slim top bar: the menu button and mark on a narrow screen, breadcrumbs (section, then page) on
 * the left, and on the right the data mode, the Evaluate action (the agent-first wizard), jobs, the help menu (guides, glossary, quickstart, system status), the theme switch and
 * the account menu. The organization lives at the top of the sidebar. In local mode the account
 * part renders nothing. A viewer or demo visitor cannot start evaluations, so the action is not
 * offered to them.
 */
export function BenchHeader({ onOpenNav, navOpen = false }: BenchHeaderProps) {
	const { t } = useTranslation("bench");
	const session = useSession();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const { data, isLoading, isError } = useGetHealthQuery();
	// An offline server never plans or starts, so it does not advertise the workflow.
	const canStart = canUse(session, "member") && pathname !== "/evaluate" && launchAvailable(data);

	return (
		<header className="flex h-13 shrink-0 items-center gap-2 px-3 md:gap-2 md:px-4" data-testid="bench-header">
			{onOpenNav && (
				<button
					type="button"
					onClick={onOpenNav}
					aria-label={t("shell.openMenu")}
					aria-expanded={navOpen}
					aria-haspopup="dialog"
					className="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-ring/60 -ml-1 flex size-8 shrink-0 items-center justify-center rounded-md focus-visible:ring-2 focus-visible:outline-none md:hidden"
					data-testid="nav-menu-button"
				>
					<Menu className="size-4.5" aria-hidden="true" />
				</button>
			)}
			<Link
				to="/"
				aria-label={t("shell.nav.home")}
				className="bg-foreground text-background focus-visible:ring-ring/60 flex size-7 shrink-0 items-center justify-center rounded-md focus-visible:ring-2 focus-visible:outline-none md:hidden"
				data-testid="header-home"
			>
				<HeimdallMark className="size-4.5" />
			</Link>
			<div className="flex min-w-0 flex-1 items-center">
				<HeaderCrumbs pathname={pathname} />
			</div>
			{IS_FIXTURE_MODE && (
				<span className="text-muted-foreground hidden text-xs sm:inline" data-testid="bench-header-fixture">
					{t("mode.fixture")}
				</span>
			)}
			<ModeBadge variant="dot" mode={data?.mode} isLoading={isLoading} isError={isError} className="hidden sm:inline-flex" />
			{canStart && (
				<Button asChild size="sm" className="max-sm:size-8 max-sm:px-0" dataTestId="header-evaluate">
					<Link to={"/evaluate" as never} aria-label={t("shell.evaluateHint")} title={t("shell.evaluateHint")}>
						<Plus aria-hidden="true" />
						<span className="hidden sm:inline">{t("shell.evaluate")}</span>
					</Link>
				</Button>
			)}
			<JobsIndicator />
			<HelpMenu />
			<span aria-hidden="true" className="bg-border mx-0.5 hidden h-5 w-px sm:block" />
			<ThemeToggle />
			<UserMenu />
		</header>
	);
}
/**
 * Where the user is: the section, then the page (a link back to the page's list when deeper).
 * Below sm only the page shows. Paths no entry owns (an auth page) show nothing.
 */
function HeaderCrumbs({ pathname }: { pathname: string }) {
	const { t } = useTranslation("bench");
	const item = activeNavItem(pathname);
	if (!item) return null;
	const group = BENCH_NAV_GROUPS.find((candidate) => candidate.items.includes(item));
	const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
	const atEntry = path === item.to;
	const Icon = item.icon;
	// The section crumb: skipped for Home (no heading) and where it would repeat the page (Settings).
	const groupLabel = group?.labelKey ? t(group.labelKey) : null;
	const showGroup = groupLabel !== null && groupLabel !== t(item.labelKey);
	return (
		<nav aria-label={t("shell.breadcrumbs")} className="min-w-0" data-testid="header-breadcrumbs">
			<ol className="flex min-w-0 items-center gap-1.5 text-sm">
				{showGroup && (
					<>
						<li className="text-muted-foreground hidden shrink-0 sm:block">{groupLabel}</li>
						<li aria-hidden="true" className="text-muted-foreground/60 hidden sm:block">
							<ChevronRight className="size-3.5" />
						</li>
					</>
				)}
				<li className="min-w-0">
					{atEntry ? (
						<span aria-current="page" className="text-foreground flex min-w-0 items-center gap-1.5 font-medium">
							<Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
							<span className="truncate">{t(item.labelKey)}</span>
						</span>
					) : (
						<Link
							to={item.to as never}
							className="text-foreground/90 hover:text-foreground flex min-w-0 items-center gap-1.5 rounded-sm font-medium transition-colors"
						>
							<Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
							<span className="truncate">{t(item.labelKey)}</span>
						</Link>
					)}
				</li>
			</ol>
		</nav>
	);
}