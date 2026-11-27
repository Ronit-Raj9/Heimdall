import { createFileRoute } from "@tanstack/react-router";
import LoginPage from "./page";

export const Route = createFileRoute("/login")({
	// return_to: where to go after signing in; checked by safeReturnTo before use. error: set by
	// the GitHub callback when sign-in did not complete (github, signup_not_allowed). reason:
	// "expired" when the tab was signed in before (loginSearch), so the page says the session ended.
	validateSearch: (search: Record<string, unknown>): { return_to?: string; error?: string; reason?: "expired" } => ({
		...(typeof search.return_to === "string" ? { return_to: search.return_to } : {}),
		...(typeof search.error === "string" ? { error: search.error } : {}),
		...(search.reason === "expired" ? { reason: "expired" as const } : {}),
	}),
	component: LoginPage,
});