import "@/components/runs/a2a/ufl4I18n";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import { pageWidthClass } from "@/components/shell/pageWidth";
import { useTranslation } from "react-i18next";
import { RunsTable } from "./runsTable";

/** /runs: every run across experiments, live runs first, filtered from the URL. */
export default function RunListView() {
	const { t } = useTranslation("u12");
	const { t: tl } = useTranslation("ufl4");
	return (
		<div className={cn(pageWidthClass("wide"), "flex flex-col gap-6")} data-page-width="wide" data-testid="run-list-screen">
			<PageHeader
				title={t("runs.title")}
				actions={
					<Button asChild size="sm" variant="outline" data-testid="runs-replay-link">
						<Link to="/replays">{tl("runs.replay")}</Link>
					</Button>
				}
			/>
			<RunsTable />
		</div>
	);
}