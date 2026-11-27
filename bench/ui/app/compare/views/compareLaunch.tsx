import { ActionCard } from "@/components/experiments/compareActionCard";
import { CompareRefsForm } from "@/components/experiments/compareRefsForm";
import { MigrateForm } from "@/components/experiments/migrateForm";
import { GitBranch, GitCompareArrows } from "lucide-react";
import { useTranslation } from "react-i18next";

/**
 * The compare page without a pair: start a new agent x models comparison (web parity L14), or
 * compare one agent at two git refs (gap lane G07). Each is a compact card; its form opens on demand.
 */
export function CompareLaunch() {
	const { t } = useTranslation("experimentActions");
	const { t: tRefs } = useTranslation("refCompare");
	return (
		<div className="grid items-start gap-3 lg:grid-cols-2">
			<ActionCard icon={GitCompareArrows} title={t("launch.title")} description={t("launch.description")} testId="compare-launch" mountWhenOpen>
				<MigrateForm testId="compare-launch-form" />
			</ActionCard>
			<ActionCard icon={GitBranch} title={tRefs("compare.title")} description={tRefs("compare.description")} testId="compare-launch-refs" mountWhenOpen>
				<CompareRefsForm testId="compare-launch-refs-form" />
			</ActionCard>
		</div>
	);
}
