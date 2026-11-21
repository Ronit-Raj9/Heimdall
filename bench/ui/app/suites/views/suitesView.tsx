import { ModeBadge } from "@/components/modeBadge";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { EmptyState, PageHeader } from "@/components/shell";
import { SuiteCards } from "@/components/suites/suiteCards";
import { Button } from "@/components/ui/button";
import { useGetSuitesQuery } from "@/lib/store";
import { cn } from "@/lib/utils";
import { Link } from "@tanstack/react-router";
import { AlertTriangle, Boxes, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The suites list: every pack in the project with its lock state and admission record, each card
 * a link to /suites/$suiteId. The old /suites?suite=X redirects to that suite's page.
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
				meta={<ModeBadge mode={data?.mode} isLoading={query.isLoading} isError={query.isError && !data} />}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						<NewSuiteButton />
						<AddSuiteButton />
					</div>
				}
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
					action={{ label: t("suites.addSuite"), to: "/suites/add" }}
					testId="suites-empty"
				/>
			) : (
				<div className={cn("min-w-0 transition-opacity", query.isFetching && "opacity-60")} aria-busy={query.isFetching}>
					<SuiteCards suites={suites} />
				</div>
			)}
		</section>
	);
}

/** The header's "New suite": write a suite from a template (/suites/new). */
export function NewSuiteButton() {
	const { t } = useTranslation("suiteAuthoring");
	return (
		<Button size="sm" variant="ghost" asChild>
			<Link to="/suites/new" data-testid="suites-new-link">
				<Plus className="size-4" aria-hidden="true" />
				{t("new.title")}
			</Link>
		</Button>
	);
}

/** The header's "Add a suite": the import and bring-your-own flow. */
export function AddSuiteButton() {
	const { t } = useTranslation("bench");
	return (
		<Button size="sm" asChild>
			<Link to="/suites/add" data-testid="suites-add-link">
				<Plus className="size-4" aria-hidden="true" />
				{t("suites.addSuite")}
			</Link>
		</Button>
	);
}