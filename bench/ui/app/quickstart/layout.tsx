import { createFileRoute } from "@tanstack/react-router";
import QuickstartPage from "./page";

export const Route = createFileRoute("/quickstart")({
	component: QuickstartPage,
});
