import { HeimdallMark } from "@/components/heimdallMark";
import { ModeBadge } from "@/components/modeBadge";
import { HelpMenu } from "@/components/platform/helpMenu";
import { JobsIndicator } from "@/components/platform/jobsDrawer";
import { UserMenu } from "@/components/platform/userMenu";
import { Button } from "@/components/ui/button";
import { BENCH_NAV_GROUPS, activeNavItem, ownPageTitle } from "@/lib/constants/navigation";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { IS_FIXTURE_MODE, useGetHealthQuery } from "@/lib/store";
import { useGetPoolStatusQuery } from "@/lib/store/apis/sharedpoolApi";
import { launchAvailable } from "@/lib/types/bench";
import type { PoolStatus } from "@/lib/types/sharedpool";
import type { SessionInfo } from "@/lib/session/sessionContext";
import "@/components/ui/ufs1I18n";
import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronRight, Menu, Moon, OctagonAlert, Plus, Sun } from "lucide-react";
import { useTheme } from "next-themes";
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
		<header className="flex h-(--header-h) shrink-0 items-center gap-2 px-3 md:gap-2 md:px-4" data-testid="bench-header">
			{onOpenNav && (
				<button
					type="button"
					onClick={onOpenNav}
					aria-label={t("shell.openMenu")}
					aria-expanded={navOpen}
					aria-haspopup="dialog"
					className="text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:ring-(--focus) -ml-1 flex size-8 shrink-0 items-center justify-center rounded-md focus-visible:ring-2 focus-visible:outline-none md:hidden"
					data-testid="nav-menu-button"
				>
					<Menu className="size-4.5" aria-hidden="true" />
				</button>
			)}
			<Link
				to="/"
				aria-label={t("shell.nav.home")}
				className="bg-foreground text-background focus-visible:ring-(--focus) flex size-7 shrink-0 items-center justify-center rounded-md focus-visible:ring-2 focus-visible:outline-none md:hidden"
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
					<Link to={"/evaluate" as never} aria-label={t("shell.evaluate")}>
						<Plus aria-hidden="true" />
						<span className="hidden sm:inline">{t("shell.evaluate")}</span>
					</Link>
				</Button>
			)}
			<JobsIndicator />
			<HelpMenu />
			<span aria-hidden="true" className="bg-border mx-0.5 hidden h-5 w-px sm:block" />
			<HeaderThemeToggle />
			<UserMenu />
		</header>
	);
}
/** True when the server-wide emergency stop is on and the visitor is signed in on a hosted install. Exported for tests. */
export function emergencyStopOn(session: Pick<SessionInfo, "authMode" | "signedIn">, pool: Pick<PoolStatus, "kill_switch"> | undefined): boolean {
	return session.authMode === "hosted" && session.signedIn && pool?.kill_switch === true;
}

/**
 * The red line under the header on every page while the emergency stop is on (M8-57), so nobody
 * wonders why runs do not start. Reads GET /pool (every member may); a failed read shows nothing.
 */
export function EmergencyStopBanner() {
	const { t } = useTranslation("ufs1");
	const session = useSession();
	const skip = session.authMode !== "hosted" || !session.signedIn;
	const pool = useGetPoolStatusQuery(undefined, { skip, pollingInterval: 60_000, skipPollingIfUnfocused: true });
	if (!emergencyStopOn(session, pool.data)) return null;
	return (
		<div
			role="alert"
			className="bg-destructive text-primary-foreground flex items-center gap-2 px-4 py-1.5 text-sm font-medium"
			data-testid="emergency-stop-banner"
		>
			<OctagonAlert className="size-4 shrink-0" aria-hidden="true" />
			{t("emergencyStop.banner")}
		</div>
	);
}

/**
 * Where the user is: the section, then the page (a link back to the page's list when deeper).
 * Below sm only the page shows. Paths no entry owns (an auth page) show nothing.
 */
export function HeaderCrumbs({ pathname }: { pathname: string }) {
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
	// A page under another entry (a job under Settings, Connect under My agents) ends with its own title.
	const own = atEntry ? null : ownPageTitle(path);
	return (
		<nav aria-label={t("shell.breadcrumbs")} className="min-w-0" data-testid="header-breadcrumbs">
			<ol className="flex min-w-0 items-center gap-1.5 text-sm">
				{showGroup && (
					<>
						<li className="text-muted-foreground hidden shrink-0 sm:block">{groupLabel}</li>
						<li aria-hidden="true" className="text-muted-foreground hidden sm:block">
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
							className="text-foreground hover:text-primary flex min-w-0 items-center gap-1.5 rounded-sm font-medium transition-colors"
						>
							<Icon className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
							<span className="truncate">{t(item.labelKey)}</span>
						</Link>
					)}
				</li>
				{own && (
					<>
						<li aria-hidden="true" className="text-muted-foreground">
							<ChevronRight className="size-3.5" />
						</li>
						<li className="min-w-0">
							<span aria-current="page" className="text-foreground block truncate font-medium">
								{own}
							</span>
						</li>
					</>
				)}
			</ol>
		</nav>
	);
}
/** The theme switch as a toggle button: its name is "Dark theme" and it reports whether dark is on. */
function HeaderThemeToggle() {
	const { t } = useTranslation("bench");
	const { resolvedTheme, setTheme } = useTheme();
	const dark = resolvedTheme === "dark";
	return (
		<Button
			variant="ghost"
			size="icon"
			className="text-muted-foreground hover:bg-accent hover:text-accent-foreground relative size-8"
			onClick={() => setTheme(dark ? "light" : "dark")}
			aria-label={t("theme.dark")}
			aria-pressed={dark}
			title={t("theme.toggle")}
			data-testid="theme-toggle-btn"
		>
			<Sun className="size-4 scale-100 rotate-0 transition-transform duration-200 dark:scale-0 dark:-rotate-90" strokeWidth={2} aria-hidden="true" />
			<Moon className="absolute size-4 scale-0 rotate-90 transition-transform duration-200 dark:scale-100 dark:rotate-0" strokeWidth={2} aria-hidden="true" />
		</Button>
	);
}
