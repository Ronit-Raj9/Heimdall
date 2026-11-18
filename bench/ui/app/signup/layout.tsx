import { createFileRoute } from "@tanstack/react-router";
import SignupPage from "./page";

export const Route = createFileRoute("/signup")({
	// invitation: the token from an invitation link, so the account joins that organization.
	validateSearch: (search: Record<string, unknown>): { invitation?: string } =>
		typeof search.invitation === "string" ? { invitation: search.invitation } : {},
	component: SignupPage,
});