import { welcomeRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): Getting started is retired; /evaluate is the one place to start.
export const Route = createFileRoute("/welcome")({
	beforeLoad: () => {
		throw redirect({ ...welcomeRedirect(), replace: true } as never);
	},
});
