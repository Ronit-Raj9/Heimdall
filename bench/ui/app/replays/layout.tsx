import { createFileRoute } from "@tanstack/react-router";
import ReplaysPage from "./page";

export const Route = createFileRoute("/replays")({
	component: ReplaysPage,
});