import { createFileRoute } from "@tanstack/react-router";
import ReleasesPage from "./page";

export const Route = createFileRoute("/releases")({
	component: ReleasesPage,
});