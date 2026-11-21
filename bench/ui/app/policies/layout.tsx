import { policiesRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): release rules are a tab of Releases; ?file= kept.
export const Route = createFileRoute("/policies")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...policiesRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
