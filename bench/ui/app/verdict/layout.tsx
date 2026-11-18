import { legacyExperimentTarget } from "@/components/experiments/legacyRoutes";
import { createFileRoute, redirect } from "@tanstack/react-router";

// The old /verdict?exp= screen is now a tab of the experiment page (UI regroup); old links land there.
export const Route = createFileRoute("/verdict")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...legacyExperimentTarget("/verdict", search as Record<string, unknown>), replace: true } as never);
	},
});