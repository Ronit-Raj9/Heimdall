import { ModeBadge } from "@/components/modeBadge";
import { JobsIndicator } from "@/components/platform/jobsDrawer";
import { OrgSwitcher } from "@/components/platform/orgSwitcher";
import { UserMenu } from "@/components/platform/userMenu";
import { ThemeToggle } from "@/components/themeToggle";
import { IS_FIXTURE_MODE, useGetHealthQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/**
 * Header: the organization (hosted mode), the data mode the server reports, jobs, the theme switch
 * and the account menu. In local mode the organization and account parts render nothing.
 */
export function BenchHeader() {
	const { t } = useTranslation("bench");
	const { data, isLoading, isError } = useGetHealthQuery();

	return (
		<header className="flex h-13 shrink-0 items-center gap-2.5 px-3 md:px-4" data-testid="bench-header">
			<div className="flex min-w-0 flex-1 items-center">
				<OrgSwitcher />
			</div>
			{IS_FIXTURE_MODE && (
				<span
					className="text-muted-foreground border-border rounded-md border border-dashed px-2 py-0.5 font-mono text-[11px]"
					data-testid="bench-header-fixture"
				>
					{t("mode.fixture")}
				</span>
			)}
			<ModeBadge mode={data?.mode} isLoading={isLoading} isError={isError} size="lg" />
			<JobsIndicator />
			<span aria-hidden="true" className="bg-border mx-0.5 h-5 w-px" />
			<ThemeToggle />
			<UserMenu />
		</header>
	);
}