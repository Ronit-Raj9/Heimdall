import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): background jobs are a tab of Settings; one job keeps its own page.
export const Route = createFileRoute("/jobs/")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("jobs", search as Record<string, unknown>), replace: true } as never);
	},
});
