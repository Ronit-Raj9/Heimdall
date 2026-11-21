import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { suiteDisplay } from "./suiteDisplay";
import { SuiteStatusPill } from "./suiteIdentity";
import { SuiteRunButton } from "./suiteRunButton";

export interface SuiteCardsProps {
	suites: readonly SuiteSummary[];
}

/**
 * The benchmarks as a plain list (lane R02): each row is the human name, one short sentence, the
 * task count and readiness, with a quiet Run that shows on hover (always on small screens). The id
 * is only in the row's hover title; image, digest, lock and probes live on the benchmark page.
 */
export function SuiteCards({ suites }: SuiteCardsProps) {
	const { t } = useTranslation("bench");
	return (
		<ul className="bg-card min-w-0 divide-y rounded-xl border" aria-label={t("suites.pickerLabel")} data-testid="suites-list">
			{suites.map((suite) => (
				<SuiteRow key={suite.id} suite={suite} />
			))}
		</ul>
	);
}

function SuiteRow({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suitesUx");
	const { name, description } = suiteDisplay(suite);
	const ref = suite.version ? `${suite.id}@${suite.version}` : suite.id;
	return (
		<li
			data-testid={`suites-row-${suite.id}`}
			data-state={suite.error ? "error" : suite.lock_state}
			className="group hover:bg-muted/40 focus-within:bg-muted/40 relative flex min-w-0 items-center gap-4 px-4 py-3 transition-colors"
		>
			<div className="flex min-w-0 flex-1 flex-col gap-0.5">
				<Link
					to="/suites/$suiteId"
					params={{ suiteId: suite.id }}
					title={ref}
					data-testid={`suites-row-${suite.id}-link`}
					className="text-foreground truncate text-sm font-medium outline-none after:absolute after:inset-0 focus-visible:underline"
				>
					{name}
				</Link>
				{suite.error ? (
					<p className="text-muted-foreground truncate text-sm" title={suite.error}>
						{suite.error}
					</p>
				) : (
					description && <p className="text-muted-foreground truncate text-sm">{description}</p>
				)}
			</div>
			<div className="flex shrink-0 items-center gap-4">
				{!suite.error && (
					<span className="text-muted-foreground hidden text-xs tabular-nums sm:inline">{t("tasks", { count: suite.task_count })}</span>
				)}
				<SuiteStatusPill suite={suite} testId={`suites-row-${suite.id}-status`} />
				{!suite.error && (
					<SuiteRunButton
						suiteId={suite.id}
						variant="ghost"
						quiet
						testId={`suites-row-${suite.id}-run`}
						className="relative z-10 md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100"
					/>
				)}
			</div>
		</li>
	);
}
