import { createFileRoute } from "@tanstack/react-router";
import VerifyEmailPage from "./page";

export const Route = createFileRoute("/verify-email")({
	// token: the one-time verification link's secret, mailed at sign-up (or logged for the server admin).
	validateSearch: (search: Record<string, unknown>): { token?: string } =>
		typeof search.token === "string" ? { token: search.token } : {},
	component: VerifyEmailPage,
});
