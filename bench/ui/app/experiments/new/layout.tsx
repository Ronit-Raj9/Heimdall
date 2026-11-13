import { createFileRoute } from "@tanstack/react-router";
import NewExperimentPage from "./page";

export const Route = createFileRoute("/experiments/new")({
	component: NewExperimentPage,
});