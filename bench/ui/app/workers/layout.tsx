import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): workers are a tab of Settings.
export const Route = createFileRoute("/workers")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("runners", search as Record<string, unknown>), replace: true } as never);
	},
});
