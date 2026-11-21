import { importTabRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): adding a benchmark is the Import tab of Benchmarks.
export const Route = createFileRoute("/suites_/add")({
	beforeLoad: () => {
		throw redirect({ ...importTabRedirect(), replace: true } as never);
	},
});
