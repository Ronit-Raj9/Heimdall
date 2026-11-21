import "./experimentsI18n";

import { RoleGate } from "@/components/platform/accessNotes";
import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ExperimentBoard } from "./experimentBoard";

/** /experiments: every experiment with its status and verdict; each row opens the experiment. */
export default function ExperimentListView() {
	const { t } = useTranslation("bench");
	const { t: tx } = useTranslation("experimentsUx");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="experiments-screen">
			<PageHeader
				title={t("experiments.list.title")}
				subtitle={tx("subtitle")}
				actions={
					<RoleGate min="member" fallback={null}>
						<Button asChild size="sm">
							<Link to="/experiments/new" data-testid="experiments-new">
								<Plus className="size-4" aria-hidden="true" />
								{t("experiments.new.title")}
							</Link>
						</Button>
					</RoleGate>
				}
			/>
			<ExperimentBoard />
		</div>
	);
}