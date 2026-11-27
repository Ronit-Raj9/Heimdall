import "./experimentsI18n";

import { PageHeader } from "@/components/shell/pageHeader";
import { Button } from "@/components/ui/button";
import { canUse, useSession } from "@/lib/session/sessionContext";
import { useGetValidityQuery } from "@/lib/store/apis/validityApi";
import "@/app/leaderboard/views/uf4bI18n";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ExperimentBoard } from "./experimentBoard";

/** /experiments: every comparison with its status and verdict; each row opens it. "New comparison" starts one. */
export default function ExperimentListView() {
	const { t } = useTranslation("bench");
	const { t: tx } = useTranslation("experimentsUx");
	const { t: tu } = useTranslation("uf4b");
	const mayRun = canUse(useSession(), "member");
	// M4-97: the Changed results page is reachable from here, with how many results changed.
	const validity = useGetValidityQuery();
	const changed = validity.data?.entries.filter((e) => e.flipped).length ?? 0;
	return (
		<div className="flex w-full flex-col gap-6" data-testid="experiments-screen">
			<PageHeader
				title={t("experiments.list.title")}
				subtitle={tx("subtitle")}
				actions={
					<div className="flex flex-wrap items-center gap-2">
						<Button asChild variant="outline" dataTestId="experiments-validity">
							<Link to={"/validity" as never}>{tu("list.changedResults", { count: changed })}</Link>
						</Button>
						{mayRun && (
							<Button asChild dataTestId="experiments-new">
								<Link to={"/experiments/new" as never}>
									<Plus aria-hidden="true" />
									{tx("newComparison")}
								</Link>
							</Button>
						)}
					</div>
				}
			/>
			<ExperimentBoard />
		</div>
	);
}