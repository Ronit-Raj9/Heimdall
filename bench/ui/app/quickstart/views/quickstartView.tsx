import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { PageHeader } from "@/components/shell";
import { useGetSuitesQuery } from "@/lib/store";
import "@/lib/types/u19I18n";
import { useTranslation } from "react-i18next";
import { MinedTasks, QuickstartSteps } from "./quickstartCards";

/** Get started: /quickstart, linked from Home. The four commands as step cards, then the mined tasks that need review. */
export default function QuickstartView() {
	const { t } = useTranslation("bench");
	const { t: tu } = useTranslation("u19");
	const { data, error, isLoading, refetch } = useGetSuitesQuery();
	return (
		<section className="mx-auto flex w-full max-w-5xl min-w-0 flex-col gap-8" data-testid="quickstart-view">
			<PageHeader
				title={tu("quickstart.title")}
				subtitle={tu("quickstart.subtitle")}
				breadcrumbs={[{ label: t("home.title"), to: "/" }, { label: tu("quickstart.title") }]}
				testId="quickstart-header"
			/>
			<QuickstartSteps />
			{isLoading ? (
				<BlockSkeleton testId="quickstart-loading" />
			) : error ? (
				<LoadError title={t("quickstart.loadError")} error={error} onRetry={refetch} testId="quickstart-error" />
			) : (
				<MinedTasks suites={data?.suites ?? []} />
			)}
		</section>
	);
}
