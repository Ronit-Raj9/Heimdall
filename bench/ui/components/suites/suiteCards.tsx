import type { SuiteSummary } from "@/lib/types/bench";
import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { hasReadyTask, suiteDisplay, suiteSections } from "./suiteDisplay";
import { SUITE_CATEGORIES, suiteCategory, type SuiteCategory } from "./suites.utils";
import "./uf6bI18n";
import { SuiteStatusPill } from "./suiteIdentity";
import { SuiteRowDelete } from "./suiteRowDelete";
import { SuiteRunButton } from "./suiteRunButton";

export interface SuiteCardsProps {
	suites: readonly SuiteSummary[];
	/** Keep the given order inside each section (a sort the user picked) instead of the library order. */
	keepOrder?: boolean;
}

/** The built-in benchmarks split by category, in the category order of the filter (M6-46). */
export function groupByCategory<T extends SuiteSummary>(suites: readonly T[]): { category: SuiteCategory; suites: T[] }[] {
	const by = new Map<SuiteCategory, T[]>();
	for (const s of suites) {
		const c = suiteCategory(s);
		by.set(c, [...(by.get(c) ?? []), s]);
	}
	return SUITE_CATEGORIES.filter((c) => by.has(c)).map((c) => ({ category: c, suites: by.get(c) ?? [] }));
}

/**
 * The benchmarks as a plain list (lane R02): each row is the human name, one short sentence, the
 * task count and readiness, with a quiet Run from md up ("Make it ready" when no task can run yet;
 * on phones the whole row opens the benchmark and the status sits under the name). The id
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

/**
 * The benchmarks list in three sections: the built-in library first ("Benchmarks", full real
 * suites), then what the org added, then the small made-up practice examples, collapsed.
 */
export function SectionedSuiteCards({ suites, keepOrder = false }: SuiteCardsProps) {
	const { t } = useTranslation("suitesUx");
	const { builtin, org, practice } = suiteSections(suites, keepOrder);
	const groups = groupByCategory(builtin);
	const heading = "text-muted-foreground text-xs font-medium tracking-wide uppercase";
	return (
		<div className="flex min-w-0 flex-col gap-6" data-testid="suites-sections">
			{builtin.length > 0 && (
				<section className="flex min-w-0 flex-col gap-2" data-testid="suites-section-builtin">
					<h2 className={heading}>{t("sections.builtin")}</h2>
					{groups.length > 1 ? (
						groups.map((g) => (
							<details key={g.category} open className="group/cat flex min-w-0 flex-col" data-testid={`suites-category-${g.category}`}>
								<summary className="text-foreground/80 flex cursor-pointer list-none items-center gap-1.5 py-1 text-sm font-medium [&::-webkit-details-marker]:hidden">
									<ChevronRight className="size-3.5 transition-transform group-open/cat:rotate-90" aria-hidden="true" />
									{t(`filter.categories.${g.category}`)} ({g.suites.length})
								</summary>
								<div className="mt-1">
									<SuiteCards suites={g.suites} />
								</div>
							</details>
						))
					) : (
						<SuiteCards suites={builtin} />
					)}
				</section>
			)}
			{org.length > 0 && (
				<section className="flex min-w-0 flex-col gap-2" data-testid="suites-section-org">
					{builtin.length > 0 && <h2 className={heading}>{t("sections.org")}</h2>}
					<SuiteCards suites={org} />
				</section>
			)}
			{practice.length > 0 && (
				<details
					className="group flex min-w-0 flex-col gap-2"
					open={builtin.length === 0 && org.length === 0}
					data-testid="suites-section-practice"
				>
					<summary className={`${heading} cursor-pointer list-none [&::-webkit-details-marker]:hidden`}>
						{t("sections.practice")} ({practice.length})
					</summary>
					<div className="mt-2">
						<SuiteCards suites={practice} />
					</div>
				</details>
			)}
		</div>
	);
}

function SuiteRow({ suite }: { suite: SuiteSummary }) {
	const { t } = useTranslation("suitesUx");
	const { t: tu } = useTranslation("uf6b");
	const { name, description } = suiteDisplay(suite);
	const ref = suite.version ? `${suite.id}@${suite.version}` : suite.id;
	const ready = hasReadyTask(suite);
	// The pill already says "24 of 89 tasks ready" when only some are ready; the count shows only otherwise (M6-45).
	const partial = (suite.admitted_count ?? 0) > 0 && suite.admitted_count < suite.task_count;
	return (
		<li
			data-testid={`suites-row-${suite.id}`}
			data-state={suite.error ? "error" : suite.lock_state}
			className="group hover:bg-muted/40 focus-within:bg-muted/40 relative flex min-w-0 flex-col items-start gap-1.5 px-4 py-3 transition-colors sm:flex-row sm:items-center sm:gap-4"
		>
			<div className="flex w-full min-w-0 flex-1 flex-col gap-0.5">
				<Link
					to="/suites/$suiteId"
					params={{ suiteId: suite.id }}
					title={ref}
					data-testid={`suites-row-${suite.id}-link`}
					className="text-foreground line-clamp-2 text-sm font-medium break-words outline-none after:absolute after:inset-0 focus-visible:underline sm:truncate"
				>
					{name}
				</Link>
				{suite.error ? (
					<p className="text-muted-foreground line-clamp-2 text-sm" title={suite.error}>
						{suite.error}
					</p>
				) : (
					description && <p className="text-muted-foreground line-clamp-2 text-sm text-pretty">{description}</p>
				)}
			</div>
			<div className="flex shrink-0 items-center gap-4">
				{!suite.error && !partial && (
					<span className="text-muted-foreground hidden text-xs tabular-nums sm:inline">{t("tasks", { count: suite.task_count })}</span>
				)}
				<SuiteStatusPill suite={suite} testId={`suites-row-${suite.id}-status`} />
				{!suite.error &&
					(ready ? (
						<SuiteRunButton
							suiteId={suite.id}
							variant="ghost"
							quiet
							testId={`suites-row-${suite.id}-run`}
							className="relative z-10 hidden md:inline-flex"
						/>
					) : (
						<Link
							to="/suites/$suiteId"
							params={{ suiteId: suite.id }}
							className="text-muted-foreground hover:text-foreground relative z-10 hidden h-7 items-center px-2 text-sm md:inline-flex"
							data-testid={`suites-row-${suite.id}-make-ready`}
						>
							{tu("list.makeReady")}
						</Link>
					))}
				<SuiteRowDelete suite={suite} name={name} />
			</div>
		</li>
	);
}
