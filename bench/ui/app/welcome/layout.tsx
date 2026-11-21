import { createFileRoute } from "@tanstack/react-router";
import WelcomePage from "./page";

export const Route = createFileRoute("/welcome")({
	component: WelcomePage,
});
