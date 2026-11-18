import { trendsRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): trends are the Trends tab of /leaderboard; old links keep their filters.
export const Route = createFileRoute("/trends")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...trendsRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});