import { MigrateForm } from "@/components/experiments/migrateForm";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { useTranslation } from "react-i18next";

/** The compare page without a pair: start a new agent x models comparison (web parity L14). */
export function CompareLaunch() {
	const { t } = useTranslation("experimentActions");
	return (
		<Card className="shadow-none" data-testid="compare-launch">
			<CardHeader>
				<CardTitle>{t("launch.title")}</CardTitle>
				<CardDescription>{t("launch.description")}</CardDescription>
			</CardHeader>
			<CardContent>
				<MigrateForm testId="compare-launch-form" />
			</CardContent>
		</Card>
	);
}
