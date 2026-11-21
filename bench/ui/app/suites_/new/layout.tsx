import { createFileRoute } from "@tanstack/react-router";
import NewSuitePage from "./page";

export const Route = createFileRoute("/suites_/new")({
	component: NewSuitePage,
});
