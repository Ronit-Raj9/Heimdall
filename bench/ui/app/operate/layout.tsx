import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): operations are a tab of Settings.
export const Route = createFileRoute("/operate")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("operations", search as Record<string, unknown>), replace: true } as never);
	},
});
