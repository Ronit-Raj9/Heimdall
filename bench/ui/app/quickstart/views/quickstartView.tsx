import { QuickstartBody } from "@/components/quickstart/quickstartPanel";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { useGetSuitesQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/** Get started: /quickstart, linked from Home. The four commands, and the mined tasks whose instructions still need review. */
export default function QuickstartView() {
	const { t } = useTranslation("bench");
	const { data, error, isLoading, refetch } = useGetSuitesQuery();
	return (
		<section className="mx-auto flex w-full max-w-5xl min-w-0 flex-col gap-6" data-testid="quickstart-view">
			<PageHeader
				title={t("home.quickstart.title")}
				subtitle={t("home.quickstart.subtitle")}
				breadcrumbs={[{ label: t("home.title"), to: "/" }, { label: t("home.quickstart.title") }]}
				testId="quickstart-header"
			/>
			{isLoading ? (
				<BlockSkeleton testId="quickstart-loading" />
			) : error ? (
				<LoadError title={t("quickstart.loadError")} error={error} onRetry={refetch} testId="quickstart-error" />
			) : (
				<QuickstartBody suites={data?.suites ?? []} />
			)}
		</section>
	);
}