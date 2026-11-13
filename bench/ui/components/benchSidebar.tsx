import { BENCH_NAV } from "@/lib/constants/navigation";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";

/** Left navigation. On narrow screens it becomes a row above the content. */
export function BenchSidebar() {
	const { t } = useTranslation("bench");

	return (
		<aside
			className="flex shrink-0 flex-col gap-4 border-b p-3 md:h-dvh md:w-60 md:border-r md:border-b-0 md:p-4"
			data-testid="bench-sidebar"
		>
			<div className="flex flex-col px-2">
				<span className="text-foreground text-sm font-semibold">{t("app.name")}</span>
				<span className="text-muted-foreground text-xs">{t("app.tagline")}</span>
			</div>
			<nav aria-label={t("nav.label")} className="flex gap-1 overflow-x-auto md:flex-col">
				{BENCH_NAV.map(({ to, labelKey, icon: Icon, testId }) => (
					<Link
						key={to}
						to={to}
						data-testid={testId}
						className={cn(
							"text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground flex items-center gap-2 rounded-sm px-2 py-1.5 text-sm font-medium whitespace-nowrap transition-colors",
							"focus-visible:ring-ring/50 focus-visible:ring-[3px]",
						)}
						activeProps={{ className: "bg-sidebar-accent text-sidebar-accent-foreground", "aria-current": "page" }}
					>
						<Icon className="size-4" aria-hidden="true" />
						{t(labelKey)}
					</Link>
				))}
			</nav>
		</aside>
	);
}