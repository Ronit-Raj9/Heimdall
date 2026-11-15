import { createFileRoute } from "@tanstack/react-router";
import ClustersPage from "./page";

export const Route = createFileRoute("/clusters")({
	component: ClustersPage,
});