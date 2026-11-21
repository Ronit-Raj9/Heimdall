import { CompareRefsForm } from "@/components/experiments/compareRefsForm";
import { MigrateForm } from "@/components/experiments/migrateForm";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useTranslation } from "react-i18next";

/**
 * The compare page without a pair: start a new agent x models comparison (web parity L14), or
 * compare one agent at two git refs (gap lane G07).
 */
export function CompareLaunch() {
	const { t } = useTranslation("experimentActions");
	const { t: tRefs } = useTranslation("refCompare");
	return (
		<div className="flex flex-col gap-4">
			<Card className="shadow-none" data-testid="compare-launch">
				<CardHeader>
					<CardTitle>{t("launch.title")}</CardTitle>
					<CardDescription>{t("launch.description")}</CardDescription>
				</CardHeader>
				<CardContent>
					<MigrateForm testId="compare-launch-form" />
				</CardContent>
			</Card>
			<Card className="shadow-none" data-testid="compare-launch-refs">
				<CardHeader>
					<CardTitle>{tRefs("compare.title")}</CardTitle>
					<CardDescription>{tRefs("compare.description")}</CardDescription>
				</CardHeader>
				<CardContent>
					<CompareRefsForm testId="compare-launch-refs-form" />
				</CardContent>
			</Card>
		</div>
	);
}
