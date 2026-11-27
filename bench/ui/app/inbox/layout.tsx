import { inboxRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// M7-1: production failures are the first tab of /failures; old links keep their tab and query.
export const Route = createFileRoute("/inbox")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...inboxRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
