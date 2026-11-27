import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
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
import { buttonVariants } from "@/components/ui/button";
import { filtersFromUrl, filtersToUrl, SUITE_SORTS, sortFromUrl, sortSuites } from "./suitesListState";
import { FilterSelect, FilterTokens, SegmentedControl, ToolbarSearch, type FilterToken } from "@/components/ui/toolbar";
import { useGetSuitesLightQuery } from "@/lib/store/apis/suitesLightApi";
import type { SuiteSummary } from "@/lib/types/bench";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, ChevronDown, Plus } from "lucide-react";
import { parseAsString, useQueryStates } from "nuqs";
import { useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";

/**
 * The benchmarks list: every pack in the project as one plain row, each a link to /suites/$suiteId. The old /suites?suite=X redirects to that suite's page.
 */
export default function SuitesView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("suitesUx");
	const query = useGetSuitesLightQuery();
	const data = query.data;
	const suites = data?.suites ?? [];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="suites-screen">
			<PageHeader title={t("suites.title")} subtitle={tu("subtitle")} actions={<AddBenchmarkMenu />} />
			{data?.mode === "synthetic" && (
				<p
					role="note"
					className="border-chart-warning/50 bg-chart-warning/10 text-foreground flex items-start gap-2 rounded-xl border px-4 py-2.5 text-sm"
					data-testid="suites-synthetic-banner"
				>
					<AlertTriangle className="text-chart-warning-ink mt-0.5 size-4 shrink-0" aria-hidden="true" />
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
	const set = (patch: Partial<SuiteFilters>) => void setUrl(filtersToUrl(patch));
	const clearAll = () => void setUrl({ q: null, category: null, source: null });
	const present = useMemo(() => {
		const seen = new Set<SuiteCategory>(suites.map((s) => suiteCategory(s)));
		return SUITE_CATEGORIES.filter((c) => seen.has(c));
	}, [suites]);
	const shown = sortSuites(filterSuites(suites, filters), sort);
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
					<span className="text-muted-foreground ml-auto text-xs tabular-nums" aria-live="polite" data-testid="suites-count">
						{tu("list.count", { shown: shown.length, total: suites.length })}
					</span>
				</div>
				<FilterTokens tokens={tokens} onClear={() => void setUrl({ category: null, source: null })} testId="suites-tokens" />
			</div>
			{shown.length === 0 ? (
				<p className="text-muted-foreground rounded-xl border border-dashed px-4 py-6 text-center text-sm" data-testid="suites-no-match">
					{t("filter.noMatch")}{" "}
					<button
						type="button"
						className="text-foreground cursor-pointer font-medium underline underline-offset-2"
						onClick={clearAll}
						data-testid="suites-clear-filters"
					>
						{t("filter.clear")}
					</button>
				</p>
			) : (
				<SectionedSuiteCards suites={shown} keepOrder={sort !== "default"} />
			)}
		</div>
	);
}

/**
 * The header's one action: "Add benchmark", a small menu of the six ways to add tasks, the same
 * six as the chooser on /suites/add (M6-41). Escape, a click outside and picking an item close it.
 */
export function AddBenchmarkMenu() {
	const { t } = useTranslation("suitesUx");
	const { t: tu } = useTranslation("uf6b");
	const ref = useRef<HTMLDetailsElement>(null);
	useEffect(() => {
		const close = () => ref.current?.removeAttribute("open");
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape" && ref.current?.open) {
				close();
				ref.current.querySelector("summary")?.focus();
			}
		};
		const onDown = (e: MouseEvent) => {
			if (ref.current?.open && !ref.current.contains(e.target as Node)) close();
		};
		document.addEventListener("keydown", onKey);
		document.addEventListener("mousedown", onDown);
		return () => {
			document.removeEventListener("keydown", onKey);
			document.removeEventListener("mousedown", onDown);
		};
	}, []);
	const item = "hover:bg-accent focus-visible:bg-accent flex flex-col gap-0.5 rounded-md px-3 py-1.5 text-sm outline-none";
	return (
		<details ref={ref} className="group relative" data-testid="suites-add-menu">
			<summary
				className={cn(buttonVariants({ size: "sm" }), "cursor-pointer list-none [&::-webkit-details-marker]:hidden")}
				data-testid="suites-add-button"
			>
				<Plus className="size-4" aria-hidden="true" />
				{t("add.button")}
				<ChevronDown className="size-3.5 opacity-70 transition-transform group-open:rotate-180" aria-hidden="true" />
			</summary>
			<div className="bg-popover text-popover-foreground absolute right-0 z-20 mt-1 flex w-72 max-w-[calc(100vw-2rem)] flex-col rounded-lg border p-1 shadow-md">
				{ADD_ENTRIES.map((e) => (
					<Link
						key={e.id}
						to={e.to}
						search={e.search as never}
						className={item}
						onClick={() => ref.current?.removeAttribute("open")}
						data-testid={`suites-add-${e.id}`}
					>
						<span className="font-medium">{tu(`chooser.${e.id}.label`)}</span>
						<span className="text-muted-foreground text-xs">{tu(`chooser.${e.id}.needs`)}</span>
					</Link>
				))}
			</div>
		</details>
	);
}
