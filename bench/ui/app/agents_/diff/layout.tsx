import { createFileRoute } from "@tanstack/react-router";
import AgentDiffPage from "./page";

// "agents_" keeps this route out of /agents, which renders no Outlet; the path is still /agents/diff.
export const Route = createFileRoute("/agents_/diff")({
	component: AgentDiffPage,
});