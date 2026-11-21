import { createFileRoute } from "@tanstack/react-router";
import IntegrationsPage from "./page";

export const Route = createFileRoute("/integrations")({
	component: IntegrationsPage,
});
