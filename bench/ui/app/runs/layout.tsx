import { legacyRunsTarget } from "@/components/experiments/legacyRoutes";
import { createFileRoute, redirect } from "@tanstack/react-router";
import RunsPage from "./page";

// /runs is the run list. The old pair view, /runs?pair=, now lives at /compare: those links redirect there.
export const Route = createFileRoute("/runs")({
	beforeLoad: ({ search }) => {
		const target = legacyRunsTarget(search as Record<string, unknown>);
		if (target) throw redirect({ ...target, replace: true } as never);
	},
	component: RunsPage,
});