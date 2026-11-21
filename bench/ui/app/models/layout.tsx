import { createFileRoute } from "@tanstack/react-router";
import ModelsPage from "./page";

export const Route = createFileRoute("/models")({
	component: ModelsPage,
});
