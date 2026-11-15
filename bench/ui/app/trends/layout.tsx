import { createFileRoute } from "@tanstack/react-router";
import TrendsPage from "./page";

export const Route = createFileRoute("/trends")({
	component: TrendsPage,
});