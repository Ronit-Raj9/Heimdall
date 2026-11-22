import { createFileRoute } from "@tanstack/react-router";
import DevicePage from "./page";

export const Route = createFileRoute("/device")({
	// code: the user code heimdall login printed (verification_uri_complete carries it).
	validateSearch: (search: Record<string, unknown>): { code?: string } => (typeof search.code === "string" ? { code: search.code } : {}),
	component: DevicePage,
});
