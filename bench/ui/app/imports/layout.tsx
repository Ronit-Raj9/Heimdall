import { createFileRoute } from "@tanstack/react-router";
import ImportsPage from "./page";

export const Route = createFileRoute("/imports")({
	component: ImportsPage,
});