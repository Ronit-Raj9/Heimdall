import { catalogRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): the catalog is a tab of Benchmarks.
export const Route = createFileRoute("/catalog")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...catalogRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
