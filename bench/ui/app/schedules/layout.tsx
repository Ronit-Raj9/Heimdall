import { schedulesRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): schedules are a tab of Scheduled checks.
export const Route = createFileRoute("/schedules")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...schedulesRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
