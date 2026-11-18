import { importsRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// UI regroup (U3): the importers are the Add a suite flow under Suites.
export const Route = createFileRoute("/imports")({
	beforeLoad: () => {
		throw redirect({ ...importsRedirect(), replace: true } as never);
	},
});