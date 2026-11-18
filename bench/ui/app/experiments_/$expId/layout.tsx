import { createFileRoute } from "@tanstack/react-router";
import ExperimentPage from "./page";

// "experiments_" keeps this route out of /experiments (the list renders no Outlet); the path is /experiments/$expId.
export const Route = createFileRoute("/experiments_/$expId")({
	component: ExperimentPage,
});