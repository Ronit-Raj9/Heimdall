import { createFileRoute } from "@tanstack/react-router";
import NewAgentPage from "./page";

export const Route = createFileRoute("/agents_/new")({
	component: NewAgentPage,
});
