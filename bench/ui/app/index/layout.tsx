import { createFileRoute } from "@tanstack/react-router";
import HomePage from "./page";

// Home at "/": what is running, what finished and which suites are ready.
export const Route = createFileRoute("/")({
	component: HomePage,
});