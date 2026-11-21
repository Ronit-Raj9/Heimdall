import { createFileRoute } from "@tanstack/react-router";
import EvaluatePage from "./page";

export const Route = createFileRoute("/evaluate")({
	component: EvaluatePage,
});
