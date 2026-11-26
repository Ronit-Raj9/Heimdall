import { createFileRoute } from "@tanstack/react-router";
import GatewayPage from "./page";

export const Route = createFileRoute("/gateway")({
	component: GatewayPage,
});