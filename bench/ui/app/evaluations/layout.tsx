import { createFileRoute } from "@tanstack/react-router";
import EvaluationsPage from "./page";

export const Route = createFileRoute("/evaluations")({
	component: EvaluationsPage,
});