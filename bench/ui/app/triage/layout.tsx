import { createFileRoute } from "@tanstack/react-router";
import TriagePage from "./page";

export const Route = createFileRoute("/triage")({
	component: TriagePage,
});