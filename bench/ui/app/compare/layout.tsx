import { createFileRoute } from "@tanstack/react-router";
import ComparePage from "./page";

export const Route = createFileRoute("/compare")({
	component: ComparePage,
});