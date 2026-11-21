import { PageHeader } from "@/components/shell/pageHeader";
import { useTranslation } from "react-i18next";
import { RunsTable } from "./runsTable";

/** /runs: every run across experiments, live runs first, filtered from the URL. */
export default function RunListView() {
	const { t } = useTranslation("u12");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="run-list-screen">
			<PageHeader title={t("runs.title")} subtitle={t("runs.subtitle")} />
			<RunsTable />
		</div>
	);
}
