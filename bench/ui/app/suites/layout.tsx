import { createFileRoute } from "@tanstack/react-router";
import SuitesPage from "./page";

export const Route = createFileRoute("/suites")({
	component: SuitesPage,
});