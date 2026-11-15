import { createFileRoute } from "@tanstack/react-router";
import WorkersPage from "./page";

export const Route = createFileRoute("/workers")({
	component: WorkersPage,
});