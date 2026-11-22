import { createFileRoute } from "@tanstack/react-router";
import StatusRoutePage from "./page";

// The public status page (lane Z14): anyone may open it, signed in or not.
export const Route = createFileRoute("/status")({
	component: StatusRoutePage,
});
