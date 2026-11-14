import { createFileRoute } from "@tanstack/react-router";
import AgentsPage from "./page";

export const Route = createFileRoute("/agents")({
	component: AgentsPage,
});