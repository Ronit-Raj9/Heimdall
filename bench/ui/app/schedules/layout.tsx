import { createFileRoute } from "@tanstack/react-router";
import SchedulesPage from "./page";

export const Route = createFileRoute("/schedules")({
	component: SchedulesPage,
});
