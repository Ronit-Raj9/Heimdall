import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): usage is a tab of Settings.
export const Route = createFileRoute("/usage")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("usage", search as Record<string, unknown>), replace: true } as never);
	},
});
