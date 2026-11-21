import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): API keys are the first tab of Settings.
export const Route = createFileRoute("/models")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("keys", search as Record<string, unknown>), replace: true } as never);
	},
});
