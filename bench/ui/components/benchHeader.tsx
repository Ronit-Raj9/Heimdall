import { HeimdallMark } from "@/components/heimdallMark";
import { ModeBadge } from "@/components/modeBadge";
import { JobsIndicator } from "@/components/platform/jobsDrawer";
import { OrgSwitcher } from "@/components/platform/orgSwitcher";
import { UserMenu } from "@/components/platform/userMenu";
import { ThemeToggle } from "@/components/themeToggle";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { IS_FIXTURE_MODE, useGetHealthQuery } from "@/lib/store";
import { Link, useRouterState } from "@tanstack/react-router";
import { Menu, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

export interface BenchHeaderProps {
	/** Opens the navigation drawer (below md, where the sidebar is hidden). */
	onOpenNav?: () => void;
	navOpen?: boolean;
}

/**
 * Header: the menu button and mark on a narrow screen, the organization (hosted mode), the data
 * mode the server reports, the New experiment action, jobs, the theme switch and the account
 * menu. In local mode the organization and account parts render nothing. A viewer or demo
 * visitor cannot start experiments, so the action is not offered to them.
 */
export function BenchHeader({ onOpenNav, navOpen = false }: BenchHeaderProps) {
	const { t } = useTranslation("bench");
	const session = useSession();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const { data, isLoading, isError } = useGetHealthQuery();
	const canStart = canUse(session, "member") && pathname !== "/experiments/new";

	return (
		<header className="flex h-13 shrink-0 items-center gap-2 px-3 md:gap-2.5 md:px-4" data-testid="bench-header">
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
				<OrgSwitcher />
			</div>
			{IS_FIXTURE_MODE && (
				<span
					className="text-muted-foreground border-border hidden rounded-md border border-dashed px-2 py-0.5 font-mono text-[11px] sm:inline"
					data-testid="bench-header-fixture"
				>
					{t("mode.fixture")}
				</span>
			)}
			<ModeBadge mode={data?.mode} isLoading={isLoading} isError={isError} size="lg" />
			{canStart && (
				<Button asChild size="sm" className="max-sm:size-8 max-sm:px-0" dataTestId="header-new-experiment">
					<Link to="/experiments/new" aria-label={t("shell.newExperiment")} title={t("shell.newExperiment")}>
						<Plus aria-hidden="true" />
						<span className="hidden sm:inline">{t("shell.newExperiment")}</span>
					</Link>
				</Button>
			)}
			<JobsIndicator />
			<span aria-hidden="true" className="bg-border mx-0.5 hidden h-5 w-px sm:block" />
			<ThemeToggle />
			<UserMenu />
		</header>
	);
}