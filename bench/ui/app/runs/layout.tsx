import { createFileRoute } from "@tanstack/react-router";
import RunsPage from "./page";

export const Route = createFileRoute("/runs")({
	component: RunsPage,
});