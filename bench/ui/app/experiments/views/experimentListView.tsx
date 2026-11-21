import "./experimentsI18n";

import { PageHeader } from "@/components/shell/pageHeader";
import { useTranslation } from "react-i18next";
import { ExperimentBoard } from "./experimentBoard";

/** /experiments: every experiment with its status and verdict; each row opens the experiment. New experiment lives in the top bar. */
export default function ExperimentListView() {
	const { t } = useTranslation("bench");
	const { t: tx } = useTranslation("experimentsUx");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="experiments-screen">
			<PageHeader title={t("experiments.list.title")} subtitle={tx("subtitle")} />
			<ExperimentBoard />
		</div>
	);
}
