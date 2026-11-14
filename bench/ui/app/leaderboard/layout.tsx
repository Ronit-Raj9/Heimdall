import { createFileRoute } from "@tanstack/react-router";
import LeaderboardPage from "./page";

export const Route = createFileRoute("/leaderboard")({
	component: LeaderboardPage,
});