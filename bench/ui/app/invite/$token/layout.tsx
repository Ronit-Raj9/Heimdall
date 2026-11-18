import { createFileRoute } from "@tanstack/react-router";
import InvitePage from "./page";

export const Route = createFileRoute("/invite/$token")({
	component: InvitePage,
});