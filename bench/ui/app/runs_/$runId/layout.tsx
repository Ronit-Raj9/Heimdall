import { createFileRoute } from "@tanstack/react-router";
import RunPage from "./page";

// "runs_" keeps this route out of /runs (the list renders no Outlet); the path is /runs/$runId.
export const Route = createFileRoute("/runs_/$runId")({
	component: RunPage,
});