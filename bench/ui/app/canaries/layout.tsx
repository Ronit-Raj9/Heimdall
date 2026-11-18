import { createFileRoute } from "@tanstack/react-router";
import CanariesPage from "./page";

export const Route = createFileRoute("/canaries")({
	component: CanariesPage,
});