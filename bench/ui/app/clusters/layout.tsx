import { clustersRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): failure clusters are the Clusters tab of /inbox; old links keep their filters.
export const Route = createFileRoute("/clusters")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...clustersRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});