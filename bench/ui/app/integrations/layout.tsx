import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): integrations are a tab of Settings.
export const Route = createFileRoute("/integrations")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("integrations", search as Record<string, unknown>), replace: true } as never);
	},
});
