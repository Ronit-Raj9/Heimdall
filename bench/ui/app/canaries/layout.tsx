import { canariesRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): drift checks are a tab of Scheduled checks; ?name= kept.
export const Route = createFileRoute("/canaries")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...canariesRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
