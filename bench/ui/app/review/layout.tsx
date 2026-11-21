import { reviewRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): the review queue is a tab of Releases; ?exp=&reason=&run= kept.
export const Route = createFileRoute("/review")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...reviewRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
