import { createFileRoute } from "@tanstack/react-router";
import ExperimentsPage from "./page";

export const Route = createFileRoute("/experiments")({
	component: ExperimentsPage,
});