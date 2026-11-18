import { QuickstartBody } from "@/components/quickstart/quickstartPanel";
import { BlockSkeleton, LoadError } from "@/components/runs/screenStates";
import { useGetSuitesQuery } from "@/lib/store";
import { useTranslation } from "react-i18next";

/** First workflow: /quickstart. The four commands, and the mined tasks whose instructions still need review. */
export default function QuickstartView() {
	const { t } = useTranslation("bench");
	const { data, error, isLoading, refetch } = useGetSuitesQuery();
	return (
		<div className="mx-auto flex w-full max-w-5xl flex-col gap-6 p-6" data-testid="quickstart-view">
			<div className="flex flex-col gap-1">
				<h1 className="text-foreground text-xl font-semibold tracking-tight">{t("quickstart.title")}</h1>
				<p className="text-muted-foreground max-w-prose text-sm">{t("quickstart.subtitle")}</p>
			</div>
			{isLoading ? (
				<BlockSkeleton testId="quickstart-loading" />
			) : error ? (
				<LoadError title={t("quickstart.loadError")} error={error} onRetry={refetch} testId="quickstart-error" />
			) : (
				<QuickstartBody suites={data?.suites ?? []} />
			)}
		</div>
	);
}
