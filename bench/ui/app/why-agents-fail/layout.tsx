import { whyAgentsFailRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// M7-1: the per-agent failure patterns are a tab of /failures; old links keep their tab and query.
export const Route = createFileRoute("/why-agents-fail")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...whyAgentsFailRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
