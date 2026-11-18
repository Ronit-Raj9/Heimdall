import { CostExplainPanel } from "@/components/migration/costWaterfall"; // W6 cost explanation
import { ExperimentParetoPanel } from "@/components/migration/migrationReport"; // W6 cost against verified success
import { parseAsString, useQueryState } from "nuqs";

/** The Cost tab: where each arm sits on cost against verified success, and why one arm cost more. */
export function CostTab({ expId }: { expId: string }) {
	const [condition] = useQueryState("condition", parseAsString);
	return (
		<div className="flex flex-col gap-6" data-testid="cost-screen">
			<ExperimentParetoPanel expId={expId} condition={condition ?? undefined} />
			<CostExplainPanel expId={expId} condition={condition ?? undefined} />
		</div>
	);
}