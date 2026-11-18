import { legacyExperimentTarget } from "@/components/experiments/legacyRoutes";
import { createFileRoute, redirect } from "@tanstack/react-router";

// The old /receipts?exp= screen is now a tab of the experiment page (UI regroup); old links land there.
export const Route = createFileRoute("/receipts")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...legacyExperimentTarget("/receipts", search as Record<string, unknown>), replace: true } as never);
	},
});