import { createFileRoute } from "@tanstack/react-router";
import ReviewPage from "./page";

export const Route = createFileRoute("/review")({
	component: ReviewPage,
});