import { adminRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): server admin is a tab of Settings (server admins only).
export const Route = createFileRoute("/admin")({
	beforeLoad: ({ search }) => {
		throw redirect({ ...adminRedirect(search as Record<string, unknown>), replace: true } as never);
	},
});
