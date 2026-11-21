import { createFileRoute } from "@tanstack/react-router";
import EvaluationPage from "./page";

// "evaluations_" keeps this route out of /evaluations (the list renders no Outlet); the path is /evaluations/$evalId.
export const Route = createFileRoute("/evaluations_/$evalId")({
	component: EvaluationPage,
});