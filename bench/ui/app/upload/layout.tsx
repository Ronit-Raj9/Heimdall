import { uploadRedirect } from "@/lib/routing/navRedirects";
import { createFileRoute, redirect } from "@tanstack/react-router";

// Agent-first nav (X09): zip upload is the Upload view of the Files tab in Settings.
export const Route = createFileRoute("/upload")({
	beforeLoad: () => {
		throw redirect({ ...uploadRedirect(), replace: true } as never);
	},
});
