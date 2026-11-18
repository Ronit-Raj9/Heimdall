import { createFileRoute } from "@tanstack/react-router";
import LivePage from "./page";

export const Route = createFileRoute("/live")({
	component: LivePage,
});
