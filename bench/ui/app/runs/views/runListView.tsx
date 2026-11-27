import { PageHeader } from "@/components/shell/pageHeader";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { useTranslation } from "react-i18next";
import { RunsTable } from "./runsTable";

/** /runs: every run across experiments, live runs first, filtered from the URL. */
export default function RunListView() {
	const { t } = useTranslation("u12");
	return (
		<div className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="run-list-screen">
			<PageHeader title={t("runs.title")} />
			<RunsTable />
		</div>
	);
}
