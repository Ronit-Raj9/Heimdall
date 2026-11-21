import { createFileRoute } from "@tanstack/react-router";
import ImprovementPage from "./page";

export const Route = createFileRoute("/improvements/$improvementId")({
	component: ImprovementPage,
});
