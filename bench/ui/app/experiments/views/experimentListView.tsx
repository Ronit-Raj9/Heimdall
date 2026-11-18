import { ExperimentList } from "@/components/experiments/experimentList";
import { PageHeader } from "@/components/shell/pageHeader";
import { useTranslation } from "react-i18next";

/** /experiments: every experiment with its status and verdict; each row opens the experiment. */
export default function ExperimentListView() {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col gap-6" data-testid="experiments-screen">
			<PageHeader title={t("experiments.list.title")} subtitle={t("experiments.list.subtitle")} />
			<ExperimentList />
		</div>
	);
}