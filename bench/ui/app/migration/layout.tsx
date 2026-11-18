import { legacyExperimentTarget } from "@/components/experiments/legacyRoutes";
import { createFileRoute, redirect } from "@tanstack/react-router";

// The old /migration?exp= screen is now a tab of the experiment page (UI regroup); old links land there.
export const Route = createFileRoute("/migration")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...legacyExperimentTarget("/migration", search as Record<string, unknown>), replace: true } as never);
	},
});