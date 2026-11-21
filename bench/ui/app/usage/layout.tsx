import { createFileRoute } from "@tanstack/react-router";
import UsagePage from "./page";

export const Route = createFileRoute("/usage")({
	component: UsagePage,
});
