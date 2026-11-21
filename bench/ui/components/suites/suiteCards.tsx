import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ListChecks } from "lucide-react";
import { useTranslation } from "react-i18next";
import { SuiteIconTile, SuiteStatusPill, suiteDisplayName } from "./suiteIdentity";
import { SuiteRunButton } from "./suiteRunButton";

export interface SuiteCardsProps {
	suites: readonly SuiteSummary[];
}

/**
 * One calm card per suite: category icon, name, a two-line description, one status pill, the
 * task count and a Run button. Image, digest, lock and probes live on the suite page's Settings.
 */
export function SuiteCards({ suites }: SuiteCardsProps) {
	const { t } = useTranslation("bench");
	return (
		<ul
			className="grid min-w-0 grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3"
			aria-label={t("suites.pickerLabel")}
			data-testid="suites-cards"
		>
			{suites.map((suite) => (
				<li key={suite.id} className="min-w-0">
					<SuiteCard suite={suite} />
				</li>
			))}
		</ul>
	);
}

function SuiteCard({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suitesUx");
	const name = suiteDisplayName(suite.id);
	return (
		<article
			data-testid={`suites-card-${suite.id}`}
			data-state={suite.error ? "error" : suite.lock_state}
			className="bg-card group focus-within:ring-ring/50 relative flex h-full min-w-0 flex-col gap-4 rounded-xl border p-5 transition-all focus-within:ring-[3px] hover:-translate-y-0.5 hover:shadow-md"
		>
			<div className="flex min-w-0 items-start gap-3">
				<SuiteIconTile suite={suite} />
				<div className="flex min-w-0 flex-1 flex-col gap-1">
					<Link
						to="/suites/$suiteId"
						params={{ suiteId: suite.id }}
						title={t("open", { suite: suite.version ? `${suite.id}@${suite.version}` : suite.id })}
						data-testid={`suites-card-${suite.id}-link`}
						className="text-foreground truncate text-base font-semibold tracking-tight outline-none after:absolute after:inset-0 after:rounded-xl"
					>
						{name}
					</Link>
					<span className="text-muted-foreground truncate font-mono text-xs">
						{suite.id}
						{suite.version && <span className="opacity-70">@{suite.version}</span>}
					</span>
				</div>
			</div>
			{suite.error ? (
				<p className="text-muted-foreground line-clamp-2 text-sm break-all" title={suite.error}>
					{suite.error}
				</p>
			) : (
				suite.description && (
					<p className="text-muted-foreground line-clamp-2 text-sm" title={suite.description}>
						{suite.description}
					</p>
				)
			)}
			<div className="mt-auto flex min-w-0 flex-wrap items-center justify-between gap-2">
				<span className="flex min-w-0 flex-wrap items-center gap-3">
					<SuiteStatusPill suite={suite} testId={`suites-card-${suite.id}-status`} />
					{!suite.error && (
						<span className="text-muted-foreground inline-flex items-center gap-1 text-xs tabular-nums">
							<ListChecks className="size-3.5" aria-hidden="true" />
							{t("tasks", { count: suite.task_count })}
						</span>
					)}
				</span>
				{!suite.error && <SuiteRunButton suiteId={suite.id} testId={`suites-card-${suite.id}-run`} className="relative z-10" />}
			</div>
		</article>
	);
}
