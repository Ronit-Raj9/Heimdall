import type { SuiteSummary } from "@/lib/types/bench";
import "@/lib/types/homeI18n";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { Pill, TrackBadge } from "./homeBits";
import { suiteReady } from "./home.utils";

/** Suites as compact tiles: category icon, name, task count and a Ready or Needs attention pill. */
export function SuitesPanel({ suites, limit = 6 }: { suites: readonly SuiteSummary[]; limit?: number }) {
	return (
		<ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-1" data-testid="home-suites-list">
			{suites.slice(0, limit).map((suite) => (
				<SuiteTile key={suite.id} suite={suite} />
			))}
		</ul>
	);
}

function SuiteTile({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("home");
	const ready = !suite.error && suiteReady(suite);
	const state = suite.error ? "failed" : ready ? "ready" : "attention";
	return (
		<li className="min-w-0" data-testid={`home-suite-${suite.id}`} data-ready={state}>
			<Link
				to="/suites/$suiteId"
				params={{ suiteId: suite.id }}
				className="bg-card hover:border-primary/30 focus-visible:ring-ring/60 flex h-full min-w-0 items-center gap-3 rounded-xl border p-3.5 shadow-(--shadow-card) transition-[box-shadow,border-color] hover:shadow-md focus-visible:ring-2 focus-visible:outline-none"
				title={suite.description || suite.id}
			>
				<TrackBadge track={suite.track} />
				<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="text-foreground truncate text-sm font-medium">{suite.id}</span>
					<span className="text-muted-foreground truncate text-xs">
						{suite.error ? t("suites.failed") : t("suites.tasks", { count: suite.task_count })}
					</span>
				</span>
				<Pill
					tone={state === "ready" ? "good" : state === "failed" ? "bad" : "warn"}
					testId={`home-suite-state-${suite.id}`}
					className="max-sm:px-1.5"
				>
					{state === "ready" ? t("suites.ready") : t("suites.attention")}
				</Pill>
			</Link>
		</li>
	);
}
