import { suiteDisplay } from "@/components/suites/suiteDisplay";
import type { SuiteSummary } from "@/lib/types/bench";
import "@/lib/types/homeI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { suiteReady } from "./home.utils";

const DOT: Record<"ready" | "attention" | "failed", string> = {
	ready: "bg-chart-success",
	attention: "bg-chart-warning",
	failed: "bg-chart-error",
};

/** Benchmarks as a plain list: human name, task count and a small Ready or Not ready yet dot. */
export function SuitesPanel({ suites, limit = 6 }: { suites: readonly SuiteSummary[]; limit?: number }) {
	return (
		<ul className="bg-card min-w-0 divide-y rounded-xl border" data-testid="home-suites-list">
			{suites.slice(0, limit).map((suite) => (
				<SuiteRow key={suite.id} suite={suite} />
			))}
		</ul>
	);
}

function SuiteRow({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("home");
	const ready = !suite.error && suiteReady(suite);
	const state = suite.error ? "failed" : ready ? "ready" : "attention";
	const { name, description } = suiteDisplay(suite);
	return (
		<li className="min-w-0" data-testid={`home-suite-${suite.id}`} data-ready={state}>
			<Link
				to="/suites/$suiteId"
				params={{ suiteId: suite.id }}
				className="hover:bg-muted/40 focus-visible:bg-muted/40 flex min-w-0 items-center gap-3 px-3.5 py-2.5 transition-colors focus-visible:outline-none"
				title={description ? `${description} (${suite.id})` : suite.id}
			>
				<span className="text-foreground min-w-0 flex-1 truncate text-sm font-medium">{name}</span>
				{!suite.error && (
					<span className="text-muted-foreground shrink-0 text-xs tabular-nums">{t("suites.tasks", { count: suite.task_count })}</span>
				)}
				<span
					className="text-muted-foreground inline-flex shrink-0 items-center gap-1.5 text-xs whitespace-nowrap"
					data-testid={`home-suite-state-${suite.id}`}
				>
					<span className={cn("size-1.5 rounded-full", DOT[state])} aria-hidden="true" />
					{state === "ready" ? t("suites.ready") : state === "failed" ? t("suites.failed") : t("suites.attention")}
				</span>
			</Link>
		</li>
	);
}
