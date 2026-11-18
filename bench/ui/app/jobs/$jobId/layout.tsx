import { createFileRoute } from "@tanstack/react-router";
import JobPage from "./page";

export const Route = createFileRoute("/jobs/$jobId")({
	component: JobPage,
});