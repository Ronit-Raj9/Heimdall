import { createFileRoute } from "@tanstack/react-router";
import AgentDetailPage from "./page";

export const Route = createFileRoute("/agents_/$agentId")({
	component: AgentDetailPage,
});
