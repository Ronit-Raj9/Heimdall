import { ModeBadge } from "@/components/modeBadge";
import { ThemeToggle } from "@/components/themeToggle";
import { IS_FIXTURE_MODE, useGetHealthQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/** Header: the data mode the server reports, and the theme switch. */
export function BenchHeader() {
	const { t } = useTranslation("bench");
	const { data, isLoading, isError } = useGetHealthQuery();

	return (
		<header className="flex h-13 shrink-0 items-center justify-end gap-2.5 px-3 md:px-4" data-testid="bench-header">
			{IS_FIXTURE_MODE && (
				<span
					className="text-muted-foreground border-border rounded-md border border-dashed px-2 py-0.5 font-mono text-[11px]"
					data-testid="bench-header-fixture"
				>
					{t("mode.fixture")}
				</span>
			)}
			<ModeBadge mode={data?.mode} isLoading={isLoading} isError={isError} size="lg" />
			<span aria-hidden="true" className="bg-border mx-0.5 h-5 w-px" />
			<ThemeToggle />
		</header>
	);
}