import { triageRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): failure triage is the Triage tab of /inbox; ?exp= and ?pattern= are kept.
export const Route = createFileRoute("/triage")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...triageRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});