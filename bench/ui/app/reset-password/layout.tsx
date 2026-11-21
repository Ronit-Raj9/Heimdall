import { createFileRoute } from "@tanstack/react-router";
import ResetPasswordPage from "./page";

export const Route = createFileRoute("/reset-password")({
	// token: the one-time reset link's secret, made by an org owner (Settings > Members).
	validateSearch: (search: Record<string, unknown>): { token?: string } =>
		typeof search.token === "string" ? { token: search.token } : {},
	component: ResetPasswordPage,
});
