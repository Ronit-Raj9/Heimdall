import { createFileRoute } from "@tanstack/react-router";
import ValidityPage from "./page";

export const Route = createFileRoute("/validity")({
	component: ValidityPage,
});