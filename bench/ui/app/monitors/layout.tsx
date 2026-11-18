import { createFileRoute } from "@tanstack/react-router";
import MonitorsPage from "./page";

export const Route = createFileRoute("/monitors")({
	component: MonitorsPage,
});