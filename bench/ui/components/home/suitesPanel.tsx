import { suiteDisplay, suiteSections } from "@/components/suites/suiteDisplay";
import type { SuiteSummary } from "@/lib/types/bench";
import "@/lib/types/homeI18n";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { suiteHomeState } from "./home.utils";

const DOT: Record<"ready" | "attention" | "failed", string> = {
	ready: "bg-chart-success",
	attention: "bg-chart-warning",
	failed: "bg-chart-error",
};

/**
 * Benchmarks as a plain list: human name, task count and a small readiness dot in the Benchmarks
 * page's words ("Ready", "24 of 89 tasks ready", "Not ready yet" only when no task is). The
 * built-in library comes first, then what the org added; the practice examples sit collapsed below.
 */
export function SuitesPanel({ suites, limit = 6 }: { suites: readonly SuiteSummary[]; limit?: number }) {
	const { t } = useTranslation("suitesUx");
	const { builtin, org, practice } = suiteSections(suites);
	const main = [...builtin, ...org];
	const list = "bg-card min-w-0 divide-y rounded-xl border";
	if (main.length === 0) {
		return (
			<ul className={list} data-testid="home-suites-list">
				{practice.slice(0, limit).map((suite) => (
					<SuiteRow key={suite.id} suite={suite} />
				))}
			</ul>
		);
	}
	return (
		<div className="flex min-w-0 flex-col gap-2">
			<ul className={list} data-testid="home-suites-list">
				{main.slice(0, limit).map((suite) => (
					<SuiteRow key={suite.id} suite={suite} />
				))}
			</ul>
			{practice.length > 0 && (
				<details className="min-w-0" data-testid="home-suites-practice">
					<summary className="text-muted-foreground cursor-pointer px-1 text-xs">
						{t("sections.practice")} ({practice.length})
					</summary>
					<ul className={`${list} mt-2`}>
						{practice.slice(0, limit).map((suite) => (
							<SuiteRow key={suite.id} suite={suite} />
						))}
					</ul>
				</details>
			)}
		</div>
	);
}

function SuiteRow({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("home");
	const { t: ts } = useTranslation("suitesUx");
	const { state, readiness } = suiteHomeState(suite);
	const label =
		state === "ready"
			? t("suites.ready")
			: state === "failed"
				? t("suites.failed")
				: readiness === "notReady"
					? t("suites.attention")
					: ts(`status.${readiness}`, { ready: suite.admitted_count, total: suite.task_count });
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
					data-readiness={readiness}
				>
					<span className={cn("size-1.5 rounded-full", DOT[state])} aria-hidden="true" />
					{label}
				</span>
			</Link>
		</li>
	);
}
