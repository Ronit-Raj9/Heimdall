import { agentDiffRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): the agent diff is the Diff tab of /agents; old links keep their query.
// "agents_" keeps this route out of /agents, which renders no Outlet; the path is still /agents/diff.
export const Route = createFileRoute("/agents_/diff")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...agentDiffRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});