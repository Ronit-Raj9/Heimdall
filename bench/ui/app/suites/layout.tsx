import { suitesListRedirect } from "@/lib/routing/u3Redirects";
import { createFileRoute, redirect } from "@tanstack/react-router";
import SuitesPage from "./page";

export const Route = createFileRoute("/suites")({
	// UI regroup (U3): the old one-page screen's ?suite=X is now the suite's own page.
	beforeLoad: ({ search }) => {
		const target = suitesListRedirect(search as Record<string, unknown>);
		if (target) throw redirect({ ...target, replace: true } as never);
	},
	component: SuitesPage,
});