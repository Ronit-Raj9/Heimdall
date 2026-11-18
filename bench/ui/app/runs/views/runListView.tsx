import { RunList } from "@/components/runs/runList";
import { PageHeader } from "@/components/shell/pageHeader";
import { useTranslation } from "react-i18next";

/** /runs: every run across experiments, newest first, filtered from the URL. */
export default function RunListView() {
	const { t } = useTranslation("bench");
	return (
		<div className="flex flex-col gap-6" data-testid="run-list-screen">
			<PageHeader title={t("runs.list.title")} subtitle={t("runs.list.subtitle")} />
			<RunList />
		</div>
	);
}