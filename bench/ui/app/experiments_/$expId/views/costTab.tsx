import { CostExplainPanel } from "@/components/migration/costWaterfall"; // W6 cost explanation
import { ExperimentParetoPanel } from "@/components/migration/migrationReport"; // W6 cost against verified success
import { GatewayOverheadPanel } from "@/components/experiments/gatewayOverheadPanel";
import { Info } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useTranslation } from "react-i18next";
import "./fw11I18n";

/**
 * The Cost tab: where each arm sits on cost against verified success, and why one arm cost more.
 * The explanation compares two arms, so a one-arm run says so instead of asking for it.
 */
export function CostTab({ expId, arms }: { expId: string; arms: readonly string[] }) {
	const { t } = useTranslation("fw11");
	const [condition] = useQueryState("condition", parseAsString);
	return (
		<div className="flex flex-col gap-6" data-testid="cost-screen">
			<ExperimentParetoPanel expId={expId} condition={condition ?? undefined} />
			{arms.length === 1 ? (
				<p
					role="note"
					className="text-muted-foreground flex items-start gap-2 rounded-lg border px-4 py-3 text-sm"
					data-testid="cost-explain-one-arm"
				>
					<Info className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
					{t("cost.oneArm")}
				</p>
			) : (
				<CostExplainPanel expId={expId} condition={condition ?? undefined} />
			)}
			<GatewayOverheadPanel expId={expId} />
		</div>
	);
}