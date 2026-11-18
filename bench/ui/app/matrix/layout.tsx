import { legacyExperimentTarget } from "@/components/experiments/legacyRoutes";
import { createFileRoute, redirect } from "@tanstack/react-router";

// The old /matrix?exp= screen is now a tab of the experiment page (UI regroup); old links land there.
export const Route = createFileRoute("/matrix")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...legacyExperimentTarget("/matrix", search as Record<string, unknown>), replace: true } as never);
	},
});