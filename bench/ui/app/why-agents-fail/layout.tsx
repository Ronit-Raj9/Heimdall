import { createFileRoute } from "@tanstack/react-router";
import WhyAgentsFailPage from "./page";

export const Route = createFileRoute("/why-agents-fail")({
	component: WhyAgentsFailPage,
});
