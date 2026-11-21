import { createFileRoute } from "@tanstack/react-router";
import ImprovementsPage from "./page";

export const Route = createFileRoute("/improvements/")({
	component: ImprovementsPage,
});
