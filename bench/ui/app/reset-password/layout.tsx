import { createFileRoute } from "@tanstack/react-router";
import ResetPasswordPage from "./page";

export const Route = createFileRoute("/reset-password")({
	component: ResetPasswordPage,
});
