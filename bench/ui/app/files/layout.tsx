import { settingsRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): project files are a tab of Settings; ?area=&dir=&path= kept.
export const Route = createFileRoute("/files")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...settingsRedirect("files", search as Record<string, unknown>), replace: true } as never);
	},
});
