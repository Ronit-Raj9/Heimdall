import { createFileRoute } from "@tanstack/react-router";
import JobsPage from "./page";

export const Route = createFileRoute("/jobs/")({
	component: JobsPage,
});
