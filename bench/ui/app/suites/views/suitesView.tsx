import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { DropdownMenu } from "@/components/ui/dropdownMenu";
import { ListCount, Pager, pageSlice } from "@/components/ui/pager";
import { isTestObject, useShowTests, withoutTests } from "@/lib/testObjects";
import { SectionedSuiteCards } from "@/components/suites/suiteCards";
import { ADD_ENTRIES } from "@/components/suites/addChooser";
import "@/components/suites/uf6bI18n";
import {
	SUITE_CATEGORIES,
	type SuiteCategory,
	type SuiteFilters,
	type SuiteSourceFilter,
	filterSuites,
	suiteCategory,
} from "@/components/suites/suites.utils";
import { Button } from "@/components/ui/button";
import { filtersFromUrl, filtersToUrl, SUITE_SORTS, sortFromUrl, sortSuites } from "./suitesListState";
import { FilterSelect, FilterTokens, SegmentedControl, ShowTestsChip, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { AlertTriangle, Boxes, ChevronDown, Plus } from "lucide-react";
import { parseAsInteger, parseAsString, useQueryState, useQueryStates } from "nuqs";
import type { ReactNode } from "react";
import { useMemo } from "react";
import { useTranslation } from "react-i18next";

/**
 * The one header of the Benchmarks hub (X-42): title, subtitle and the Add menu, with the hub's
 * tabs passed in so they sit under the title. `app/suites/page.tsx` renders it above the tabs;
 * the list view itself has no header of its own.
 */
export function SuitesPageHeader({ tabs }: { tabs?: ReactNode }) {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("suitesUx");
	return <PageHeader title={t("suites.title")} subtitle={tu("subtitle")} actions={<AddBenchmarkMenu />} tabs={tabs} />;
}

/**
 * The benchmarks list: every pack in the project as one plain row, each a link to /suites/$suiteId. The old /suites?suite=X redirects to that suite's page.
 */
export default function SuitesView() {
	const { t } = useTranslation("bench");
	const query = useGetSuitesLightQuery();
	const data = query.data;
	const suites = data?.suites ?? [];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="suites-screen">
			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-warning bg-warning-soft text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
					data-testid="suites-synthetic-banner"
				>
					<AlertTriangle className="text-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("suites.synthetic")}
				</p>
			)}
			{query.isLoading ? (
				<BlockSkeleton rows={6} testId="suites-loading" />
			) : query.isError || !data ? (
				<LoadError title={t("suites.loadError")} error={query.error} onRetry={() => void query.refetch()} testId="suites-error" />
			) : suites.length === 0 ? (
				<EmptyState
					icon={Boxes}
					message={t("suites.empty")}
					action={{ label: t("suites.addSuite"), to: "/suites/add" }}
					testId="suites-empty"
				/>
			) : (
				<div className={cn("min-w-0 transition-opacity", query.isFetching && "opacity-60")} aria-busy={query.isFetching}>
					<SuitesList suites={suites} />
				</div>
			)}
		</section>
	);
}

const SOURCE_FILTERS: readonly SuiteSourceFilter[] = ["all", "builtin", "team"];
const SOURCE_KEY = {
	all: "filter.sourceAll",
	builtin: "filter.sourceBuiltin",
	team: "filter.sourceTeam",
} as const satisfies Record<SuiteSourceFilter, string>;
const CATEGORY_KEY = {
	terminal: "filter.categories.terminal",
	coding: "filter.categories.coding",
	support: "filter.categories.support",
	research: "filter.categories.research",
	memory: "filter.categories.memory",
	safety: "filter.categories.safety",
	team: "filter.categories.team",
	voice: "filter.categories.voice",
	other: "filter.categories.other",
} as const satisfies Record<SuiteCategory, string>;

/**
 * My benchmarks with the Catalog tab's toolbar: search (id, name, description), a category filter,
 * a source filter (built in or added by the team) and a sort, then the sectioned list of what
 * matches with a count. Search, filters and sort live in the URL, so Back and reload keep them (M6-43).
 */
export function SuitesList({ suites }: { suites: readonly SuiteSummary[] }) {
	const { t } = useTranslation("suitesUx");
	const { t: tu } = useTranslation("uf6b");
	const [url, setUrl] = useQueryStates(
		{ q: parseAsString, category: parseAsString, source: parseAsString, sort: parseAsString },
		{ history: "replace", throttleMs: 200 },
	);
	const filters = filtersFromUrl(url);
	const sort = sortFromUrl(url.sort);
	const [page, setPage] = useQueryState("page", parseAsInteger.withDefault(1).withOptions({ history: "replace" }));
	const [showTests, setShowTests] = useShowTests();
	// A filter change starts again at page 1.
	const set = (patch: Partial<SuiteFilters>) => {
		void setUrl(filtersToUrl(patch));
		void setPage(null);
	};
	const clearAll = () => {
		void setUrl({ q: null, category: null, source: null });
		void setPage(null);
	};
	const present = useMemo(() => {
		const seen = new Set<SuiteCategory>(suites.map((s) => suiteCategory(s)));
		return SUITE_CATEGORIES.filter((c) => seen.has(c));
	}, [suites]);
	// Test benchmarks (zz-test-*, stand-in descriptions) are hidden unless ?tests=1 (M1-18).
	const { shown: real, hidden } = withoutTests(suites, (s) => isTestObject(s.id, s), showTests);
	const shown = sortSuites(filterSuites(real, filters), sort);
	const slice = pageSlice(shown, page);
	const tokens = (
		[
			filters.category && {
				key: "category",
				label: `${t("filter.category")}: ${t(CATEGORY_KEY[filters.category])}`,
				onRemove: () => set({ category: null }),
			},
			filters.source !== "all" && { key: "source", label: t(SOURCE_KEY[filters.source]), onRemove: () => set({ source: "all" }) },
		] as (FilterToken | false | null)[]
	).filter((x): x is FilterToken => Boolean(x));
	return (
		<div className="flex min-w-0 flex-col gap-4">
			<div className="flex flex-col gap-2">
				<div className="flex flex-wrap items-center gap-2" role="toolbar" data-testid="suites-toolbar">
					<ToolbarSearch
						value={filters.query}
						onChange={(v) => set({ query: v })}
						placeholder={t("filter.search")}
						label={t("filter.search")}
						testId="suites-search"
						className="[&_input]:h-9"
					/>
					<FilterSelect
						label={t("filter.category")}
						allLabel={t("filter.all")}
						options={present.map((c) => ({ value: c, label: t(CATEGORY_KEY[c]) }))}
						value={filters.category}
						onChange={(v) => set({ category: (v as SuiteCategory | null) ?? null })}
						testId="suites-filter-category"
						className="h-9"
					/>
					<SegmentedControl
						label={t("filter.source")}
						options={SOURCE_FILTERS.map((s) => ({ value: s, label: t(SOURCE_KEY[s]) }))}
						value={filters.source}
						onChange={(v) => set({ source: v })}
						testId="suites-source"
						className="h-9"
					/>
					<FilterSelect
						label={tu("list.sort")}
						allLabel={tu("list.sortDefault")}
						options={SUITE_SORTS.filter((v) => v !== "default").map((v) => ({ value: v, label: tu(`list.sorts.${v as "name" | "tasks" | "ready"}`) }))}
						value={sort === "default" ? null : sort}
						onChange={(v) => void setUrl({ sort: v ?? null })}
						testId="suites-sort"
						className="h-9"
					/>
					<ShowTestsChip count={hidden} on={showTests} onChange={setShowTests} testId="suites-show-tests" />
					<span className="ml-auto" aria-live="polite">
						<ListCount shown={shown.length} total={real.length} hidden={hidden} testId="suites-count" />
					</span>
				</div>
				<FilterTokens tokens={tokens} onClear={() => void setUrl({ category: null, source: null })} testId="suites-tokens" />
			</div>
			{shown.length === 0 ? (
				<EmptyState
					kind="filtered"
					body={t("filter.noMatch")}
					action={{ label: t("filter.clear"), onClick: clearAll }}
					testId="suites-no-match"
				/>
			) : (
				<>
					<SectionedSuiteCards suites={slice.rows} keepOrder={sort !== "default"} />
					<Pager
						page={slice.page}
						pageCount={slice.pageCount}
						onPage={(p) => void setPage(p)}
						rangeText={tu("list.pageOf", { first: slice.first, last: slice.last, total: shown.length })}
						testId="suites-pager"
					/>
				</>
			)}
		</div>
	);
}

/**
 * The header's one action: "Add benchmark", a menu of the six ways to add tasks, the same six as
 * the chooser on /suites/add (M6-41). The shared dropdown gives arrow keys, Escape and click-outside.
 */
export function AddBenchmarkMenu() {
	const { t } = useTranslation("suitesUx");
	const { t: tu } = useTranslation("uf6b");
	return (
		<DropdownMenu
			label={t("add.button")}
			trigger={(p) => (
				<Button {...p} size="sm" dataTestId="suites-add-button">
					<Plus className="size-4" aria-hidden="true" />
					{t("add.button")}
					<ChevronDown className="size-3.5" aria-hidden="true" />
				</Button>
			)}
			items={ADD_ENTRIES.map((e) => ({
				id: e.id,
				label: (
					<span className="flex flex-col gap-0.5 py-0.5">
						<span className="font-medium">{tu(`chooser.${e.id}.label`)}</span>
						<span className="text-muted-foreground text-xs">{tu(`chooser.${e.id}.needs`)}</span>
					</span>
				),
				to: e.to,
				search: e.search as Record<string, unknown> | undefined,
				testId: `suites-add-${e.id}`,
			}))}
			className="max-w-[calc(100vw-2rem)]"
			testId="suites-add-menu"
		/>
	);
}
