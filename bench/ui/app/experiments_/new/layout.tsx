import { createFileRoute } from "@tanstack/react-router";
import NewExperimentPage from "./page";

// "experiments_" keeps this route out of /experiments (the list renders no Outlet); the path is still /experiments/new.
export const Route = createFileRoute("/experiments_/new")({
	component: NewExperimentPage,
});