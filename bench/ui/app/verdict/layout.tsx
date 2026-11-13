import { createFileRoute } from "@tanstack/react-router";
import VerdictPage from "./page";

export const Route = createFileRoute("/verdict")({
	component: VerdictPage,
});