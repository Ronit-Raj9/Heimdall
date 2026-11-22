import "./experimentsI18n";

import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ExperimentBoard } from "./experimentBoard";

/** /experiments: every comparison with its status and verdict; each row opens it. "New comparison" starts one. */
export default function ExperimentListView() {
	const { t } = useTranslation("bench");
	const { t: tx } = useTranslation("experimentsUx");
	const mayRun = canUse(useSession(), "member");
	return (
		<div className="mx-auto flex w-full max-w-[1200px] flex-col gap-6" data-testid="experiments-screen">
			<PageHeader
				title={t("experiments.list.title")}
				subtitle={tx("subtitle")}
				actions={
					mayRun ? (
						<Button asChild dataTestId="experiments-new">
							<Link to={"/experiments/new" as never}>
								<Plus aria-hidden="true" />
								{tx("newComparison")}
							</Link>
						</Button>
					) : undefined
				}
			/>
			<ExperimentBoard />
		</div>
	);
}
