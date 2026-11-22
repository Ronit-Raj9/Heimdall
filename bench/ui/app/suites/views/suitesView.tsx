import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { SectionedSuiteCards } from "@/components/suites/suiteCards";
import { buttonVariants } from "@/components/ui/button";
import { useGetSuitesQuery } from "@/lib/store";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, ChevronDown, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The benchmarks list: every pack in the project as one plain row, each a link to /suites/$suiteId. The old /suites?suite=X redirects to that suite's page.
 */
export default function SuitesView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("suitesUx");
	const query = useGetSuitesQuery();
	const data = query.data;
	const suites = data?.suites ?? [];
	return (
		<section className="mx-auto flex w-full max-w-[1200px] min-w-0 flex-col gap-8" data-testid="suites-screen">
			<PageHeader
				title={t("suites.title")}
				subtitle={tu("subtitle")}
				actions={<AddBenchmarkMenu />}
			/>
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
					action={{ label: t("suites.addSuite"), to: "/suites", search: { tab: "import" } }}
					testId="suites-empty"
				/>
			) : (
				<div className={cn("min-w-0 transition-opacity", query.isFetching && "opacity-60")} aria-busy={query.isFetching}>
					<SectionedSuiteCards suites={suites} />
				</div>
			)}
		</section>
	);
}

/** The header's one action: "Add benchmark", a small menu of From catalog and Create your own. */
export function AddBenchmarkMenu() {
	const { t } = useTranslation("suitesUx");
	const item =
		"hover:bg-accent focus-visible:bg-accent block rounded-md px-3 py-1.5 text-sm whitespace-nowrap outline-none";
	return (
		<details className="group relative" data-testid="suites-add-menu">
			<summary
				className={cn(
					buttonVariants({ size: "sm" }),
					"cursor-pointer list-none [&::-webkit-details-marker]:hidden",
				)}
				data-testid="suites-add-button"
			>
				<Plus className="size-4" aria-hidden="true" />
				{t("add.button")}
				<ChevronDown className="size-3.5 opacity-70 transition-transform group-open:rotate-180" aria-hidden="true" />
			</summary>
			<div className="bg-popover text-popover-foreground absolute right-0 z-20 mt-1 flex min-w-44 flex-col rounded-lg border p-1 shadow-md">
				<Link to="/suites/add" className={item} data-testid="suites-add-anything">
					{t("add.anything")}
				</Link>
				<Link to="/suites" search={{ tab: "catalog" } as never} className={item} data-testid="suites-add-catalog">
					{t("add.catalog")}
				</Link>
				<Link to="/suites/new" className={item} data-testid="suites-new-link">
					{t("add.create")}
				</Link>
			</div>
		</details>
	);
}
